package deploy

import (
	"fmt"
	"strings"

	"github.com/creasty/defaults"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
)

type ServiceApp struct {
	Image    *gcp.Image
	CloudRun *gcp.CloudRun

	App        *apiv1.App
	Props      *types.ServiceAppProperties
	DeployOpts *ServiceAppDeployOptions
}

type ServiceAppArgs struct {
	ProjectID string
	Region    string
	Env       map[string]string
	Vars      map[string]interface{}
	Databases []*DatabaseDep
}

type ServiceAppDeployOptions struct {
	CPULimit    float64 `mapstructure:"cpu_limit" default:"1"`
	MemoryLimit int     `mapstructure:"memory_limit" default:"128"`
	MinScale    int     `mapstructure:"min_scale" default:"0"`
	MaxScale    int     `mapstructure:"max_scale" default:"100"`
}

func NewServiceAppDeployOptions(in map[string]interface{}) (*ServiceAppDeployOptions, error) {
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
		validation.Field(&o.CPULimit, validation.In(1.0, 2.0, 4.0)),
		validation.Field(&o.MemoryLimit, validation.Min(128), validation.Max(8192)),
		validation.Field(&o.MinScale, validation.Min(0), validation.Max(100)),
		validation.Field(&o.MaxScale, validation.Min(1)),
	)
}

func NewServiceApp(plan *apiv1.AppPlan) (*ServiceApp, error) {
	opts, err := types.NewServiceAppProperties(plan.State.App.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	deployOpts, err := NewServiceAppDeployOptions(plan.State.App.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	return &ServiceApp{
		App:        plan.State.App,
		Props:      opts,
		DeployOpts: deployOpts,
	}, nil
}

func (o *ServiceApp) Plan(pctx *config.PluginContext, r *registry.Registry, c *ServiceAppArgs) error {
	// Add GCR docker image.
	o.Image = &gcp.Image{
		Name:      fields.Sprintf("%s/%s", plugin_util.SanitizeName(pctx.Env().Env()), plugin_util.SanitizeName(o.App.Id)),
		ProjectID: fields.String(c.ProjectID),
		GCR:       fields.String(gcp.RegionToGCR(c.Region)),
		Source:    fields.String(o.Props.LocalDockerImage),
		Pull:      false,
	}

	if o.Props.LocalDockerHash != "" {
		o.Image.SourceHash = fields.String(o.Props.LocalDockerHash)
	}

	_, err := r.RegisterAppResource(o.App, o.Props.LocalDockerImage, o.Image)
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
		cloudSQLconnNames[i] = db.CloudSQL.ConnectionName
	}

	o.CloudRun = &gcp.CloudRun{
		Name:      gcp.IDField(pctx.Env(), o.App.Id),
		Port:      fields.Int(o.Props.Container.Port),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		Image:     o.Image.ImageName(),
		IsPublic:  fields.Bool(o.Props.Public),
		EnvVars:   fields.Map(envVars),

		CloudSQLInstances: fields.Sprintf(strings.Join(cloudSQLconnFmt, ","), cloudSQLconnNames...),
		MinScale:          fields.Int(o.DeployOpts.MinScale),
		MaxScale:          fields.Int(o.DeployOpts.MaxScale),
		MemoryLimit:       fields.String(fmt.Sprintf("%dMi", o.DeployOpts.MemoryLimit)),
		CPULimit:          fields.String(fmt.Sprintf("%dm", int(o.DeployOpts.CPULimit*1000))),
	}

	_, err = r.RegisterAppResource(o.App, "cloud_run", o.CloudRun)
	if err != nil {
		return err
	}

	return nil
}
