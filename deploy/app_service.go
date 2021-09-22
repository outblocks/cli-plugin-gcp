package deploy

import (
	"fmt"
	"path/filepath"

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

	App  *types.App
	Opts *ServiceAppOptions
}

type ServiceAppArgs struct {
	ProjectID string
	Region    string
	Env       map[string]string
}

func NewServiceApp(app *types.App) *ServiceApp {
	opts := &ServiceAppOptions{}
	if err := opts.Decode(app.Properties); err != nil {
		panic(err)
	}

	return &ServiceApp{
		App:  app,
		Opts: opts,
	}
}

type ServiceAppOptions struct {
	Build struct {
		Dockerfile    string `mapstructure:"dockerfile"`
		DockerContext string `mapstructure:"context"`
	} `mapstructure:"build"`

	LocalDockerImage string `mapstructure:"local_docker_image"`
	LocalDockerHash  string `mapstructure:"local_docker_hash"`

	CDN struct {
		Enabled bool `mapstructure:"enabled"`
	} `mapstructure:"cdn"`
}

func (o *ServiceAppOptions) Decode(in interface{}) error {
	return mapstructure.Decode(in, o)
}

func (o *ServiceApp) Plan(pctx *config.PluginContext, r *registry.Registry, c *ServiceAppArgs, verify bool) error {
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

	envVars := make(map[string]fields.Field, len(c.Env))
	for k, v := range c.Env {
		envVars[k] = fields.String(v)
	}

	// Add cloud run service.
	o.CloudRun = &gcp.CloudRun{
		Name:      fields.String(gcp.ID(pctx.Env().ProjectID(), o.App.ID)),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		Image:     o.Image.ImageName(),
		IsPublic:  fields.Bool(true),
		EnvVars:   fields.Map(envVars),
	}

	err = r.Register(o.CloudRun, o.App.ID, "cloud_run")
	if err != nil {
		return err
	}

	return nil
}
