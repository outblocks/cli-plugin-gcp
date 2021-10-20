package deploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/creasty/defaults"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
)

type ServiceApp struct {
	Image    *gcp.Image
	CloudRun *gcp.CloudRun

	App        *types.App
	Opts       *ServiceAppOptions
	DeployOpts *ServiceAppDeployOptions
}

type ServiceAppArgs struct {
	ProjectID string
	Region    string
	Env       map[string]string
	Vars      map[string]interface{}
	Databases []*DatabaseDep
}

func NewServiceApp(plan *types.AppPlan) (*ServiceApp, error) {
	opts, err := NewServiceAppOptions(plan.App.Properties)
	if err != nil {
		return nil, err
	}

	deployOpts, err := NewServiceAppDeployOptions(plan.Properties)
	if err != nil {
		return nil, err
	}

	return &ServiceApp{
		App:        plan.App,
		Opts:       opts,
		DeployOpts: deployOpts,
	}, nil
}

type ServiceAppDeployOptions struct {
	CPULimit    int `mapstructure:"cpu-limit" default:"1"`
	MemoryLimit int `mapstructure:"memory-limit" default:"128"`
	MinScale    int `mapstructure:"min-scale" default:"0"`
	MaxScale    int `mapstructure:"max-scale" default:"100"`
}

func NewServiceAppDeployOptions(in interface{}) (*ServiceAppDeployOptions, error) {
	o := &ServiceAppDeployOptions{}

	err := mapstructure.Decode(in, o)
	if err != nil {
		return nil, err
	}

	err = defaults.Set(o)
	if err != nil {
		return nil, err
	}

	return o, validation.ValidateStruct(o,
		validation.Field(&o.CPULimit, validation.In(1, 2, 4)),
		validation.Field(&o.MemoryLimit, validation.Min(128), validation.Max(8192)),
		validation.Field(&o.MinScale, validation.Min(0), validation.Max(100)),
		validation.Field(&o.MaxScale, validation.Min(1)),
	)
}

type ServiceAppOptions struct {
	Build struct {
		Dockerfile    string `mapstructure:"dockerfile"`
		DockerContext string `mapstructure:"context"`
	} `mapstructure:"build"`

	Container struct {
		Port int `mapstructure:"port" default:"80"`
	} `mapstructure:"container"`

	LocalDockerImage string `mapstructure:"local_docker_image"`
	LocalDockerHash  string `mapstructure:"local_docker_hash"`

	CDN struct {
		Enabled bool `mapstructure:"enabled"`
	} `mapstructure:"cdn"`
}

func NewServiceAppOptions(in interface{}) (*ServiceAppOptions, error) {
	o := &ServiceAppOptions{}

	err := mapstructure.Decode(in, o)
	if err != nil {
		return nil, err
	}

	return o, defaults.Set(o)
}

func (o *ServiceApp) Plan(pctx *config.PluginContext, r *registry.Registry, c *ServiceAppArgs) error {
	dockerfile := filepath.Join(o.App.Dir, o.Opts.Build.Dockerfile)

	if !plugin_util.FileExists(dockerfile) {
		return fmt.Errorf("app '%s' dockerfile '%s' does not exist", o.App.Name, dockerfile)
	}

	// Add GCR docker image.
	o.Image = &gcp.Image{
		Name:      fields.String(o.Opts.LocalDockerImage),
		ProjectID: fields.String(c.ProjectID),
		GCR:       fields.String(gcp.RegionToGCR(c.Region)),
		Source:    fields.String(o.Opts.LocalDockerImage),
		Pull:      false,
	}

	if o.Opts.LocalDockerHash != "" {
		o.Image.SourceHash = fields.String(o.Opts.LocalDockerHash)
	}

	err := r.Register(o.Image, o.App.ID, o.Opts.LocalDockerImage)
	if err != nil {
		return err
	}

	// Expand env vars.
	envVars := make(map[string]fields.Field, len(c.Env))
	eval := fields.NewFieldVarEvaluator(c.Vars)

	for k, v := range c.Env {
		exp, err := eval.Expand(v)
		if err != nil {
			return err
		}

		envVars[k] = exp
	}

	// Add cloud run service.
	cloudSQLconnFmt := make([]string, len(c.Databases))
	cloudSQLconnNames := make([]interface{}, len(c.Databases))

	for i, db := range c.Databases {
		cloudSQLconnFmt[i] = "%s"
		cloudSQLconnNames[i] = db.CloudSQL.ConnectionName.Input()
	}

	o.CloudRun = &gcp.CloudRun{
		Name:      fields.String(gcp.ID(pctx.Env().ProjectID(), o.App.ID)),
		Port:      fields.Int(o.Opts.Container.Port),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		Image:     o.Image.ImageName(),
		IsPublic:  fields.Bool(true),
		EnvVars:   fields.Map(envVars),

		CloudSQLInstances: fields.Sprintf(strings.Join(cloudSQLconnFmt, ","), cloudSQLconnNames...),
		MinScale:          fields.Int(o.DeployOpts.MinScale),
		MaxScale:          fields.Int(o.DeployOpts.MaxScale),
		MemoryLimit:       fields.String(fmt.Sprintf("%dMi", o.DeployOpts.MemoryLimit)),
		CPULimit:          fields.String(fmt.Sprintf("%dm", o.DeployOpts.CPULimit*1000)),
	}

	err = r.Register(o.CloudRun, o.App.ID, "cloud_run")
	if err != nil {
		return err
	}

	return nil
}
