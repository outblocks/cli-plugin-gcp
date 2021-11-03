package deploy

import (
	"fmt"
	"mime"
	"path/filepath"

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

type StaticApp struct {
	Bucket   *gcp.Bucket
	Files    []*gcp.BucketObject
	Image    *gcp.Image
	CloudRun *gcp.CloudRun

	App        *types.App
	Props      *types.StaticAppProperties
	DeployOpts *StaticAppDeployOptions
}

type StaticAppArgs struct {
	ProjectID string
	Region    string
}

type StaticAppDeployOptions struct {
	MinScale int `mapstructure:"min_scale" default:"0"`
	MaxScale int `mapstructure:"max_scale" default:"100"`
}

func NewStaticAppDeployOptions(in interface{}) (*StaticAppDeployOptions, error) {
	o := &StaticAppDeployOptions{}

	err := mapstructure.Decode(in, o)
	if err != nil {
		return nil, err
	}

	err = defaults.Set(o)
	if err != nil {
		return nil, err
	}

	return o, validation.ValidateStruct(o,
		validation.Field(&o.MinScale, validation.Min(0), validation.Max(100)),
		validation.Field(&o.MaxScale, validation.Min(1)),
	)
}

func NewStaticApp(plan *types.AppPlan) (*StaticApp, error) {
	opts, err := types.NewStaticAppProperties(plan.App.Properties)
	if err != nil {
		return nil, err
	}

	deployOpts, err := NewStaticAppDeployOptions(plan.App.Properties)
	if err != nil {
		return nil, err
	}

	return &StaticApp{
		App:        plan.App,
		Props:      opts,
		DeployOpts: deployOpts,
	}, nil
}

func (o *StaticApp) Plan(pctx *config.PluginContext, r *registry.Registry, c *StaticAppArgs) error {
	buildDir := filepath.Join(o.App.Dir, o.Props.Build.Dir)

	buildPath, ok := plugin_util.CheckDir(buildDir)
	if !ok {
		return fmt.Errorf("app '%s' build dir '%s' does not exist", o.App.Name, buildDir)
	}

	// Add bucket.
	o.Bucket = &gcp.Bucket{
		Name:       fields.String(gcp.GlobalID(pctx.Env().ProjectID(), c.ProjectID, o.App.ID)),
		Location:   fields.String(c.Region),
		ProjectID:  fields.String(c.ProjectID),
		Versioning: fields.Bool(false),
	}

	err := r.RegisterAppResource(o.App, "bucket", o.Bucket)
	if err != nil {
		return err
	}

	// Add bucket contents.
	files, err := findFiles(buildPath)
	if err != nil {
		return err
	}

	for filePath, hash := range files {
		path := filepath.Join(buildPath, filePath)

		obj := &gcp.BucketObject{
			BucketName:  o.Bucket.Name,
			Name:        fields.String(filePath),
			Hash:        fields.String(hash),
			Path:        path,
			IsPublic:    fields.Bool(true),
			ContentType: fields.String(mime.TypeByExtension(filepath.Ext(path))),
		}

		if !o.Props.CDN.Enabled {
			obj.CacheControl = fields.String("private, max-age=0, no-transform")
		}

		err = r.RegisterAppResource(o.App, filePath, obj)
		if err != nil {
			return err
		}

		o.Files = append(o.Files, obj)
	}

	// Add GCR docker image.
	o.Image = &gcp.Image{
		Name:      fields.String(gcp.GCSProxyImageName),
		Tag:       fields.String(gcp.GCSProxyVersion),
		ProjectID: fields.String(c.ProjectID),
		GCR:       fields.String(gcp.RegionToGCR(c.Region)),
		Source:    fields.String(gcp.GCSProxyDockerImage),
		Pull:      true,
	}

	err = r.RegisterPluginResource(CommonName, gcp.GCSProxyImageName, o.Image)
	if err != nil {
		return err
	}

	envVars := map[string]fields.Field{
		"GCS_BUCKET":  o.Bucket.Name,
		"FORCE_HTTPS": fields.String("1"),
		"ROUTING":     fields.String(o.Props.Routing),
	}

	// Add cloud run service.
	o.CloudRun = &gcp.CloudRun{
		Name:      fields.String(gcp.ID(pctx.Env().ProjectID(), o.App.ID)),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		Image:     o.Image.ImageName(),
		IsPublic:  fields.Bool(true),
		EnvVars:   fields.Map(envVars),

		MinScale: fields.Int(o.DeployOpts.MinScale),
		MaxScale: fields.Int(o.DeployOpts.MaxScale),
	}

	err = r.RegisterAppResource(o.App, "cloud_run", o.CloudRun)
	if err != nil {
		return err
	}

	return nil
}
