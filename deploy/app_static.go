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
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
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

	App        *apiv1.App
	SkipFiles  bool
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

func NewStaticAppDeployOptions(in map[string]interface{}) (*StaticAppDeployOptions, error) {
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

func NewStaticApp(plan *apiv1.AppPlan) (*StaticApp, error) {
	opts, err := types.NewStaticAppProperties(plan.State.App.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	deployOpts, err := NewStaticAppDeployOptions(plan.State.App.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	return &StaticApp{
		App:        plan.State.App,
		SkipFiles:  plan.Skip,
		Props:      opts,
		DeployOpts: deployOpts,
	}, nil
}

func (o *StaticApp) Plan(pctx *config.PluginContext, r *registry.Registry, c *StaticAppArgs) error {
	buildDir := filepath.Join(pctx.Env().ProjectDir(), o.App.Dir, o.Props.Build.Dir)

	// Add bucket.
	o.Bucket = &gcp.Bucket{
		Name:       gcp.GlobalIDField(pctx.Env(), c.ProjectID, o.App.Id),
		Location:   fields.String(c.Region),
		ProjectID:  fields.String(c.ProjectID),
		Versioning: fields.Bool(false),
	}

	_, err := r.RegisterAppResource(o.App, "bucket", o.Bucket)
	if err != nil {
		return err
	}

	// Add bucket contents.
	if !o.SkipFiles {
		buildPath, ok := plugin_util.CheckDir(buildDir)
		if !ok {
			return fmt.Errorf("app '%s' build dir '%s' does not exist", o.App.Name, buildDir)
		}

		files, err := findFiles(buildPath)
		if err != nil {
			return err
		}

		for filePath, hash := range files {
			path := filepath.Join(buildPath, filePath)

			contentType := mime.TypeByExtension(filepath.Ext(path))
			if contentType == "" {
				contentType = "text/plain; charset=utf-8"
			}

			obj := &gcp.BucketObject{
				BucketName:  o.Bucket.Name,
				Name:        fields.String(filePath),
				Hash:        fields.String(hash),
				Path:        path,
				IsPublic:    fields.Bool(true),
				ContentType: fields.String(contentType),
			}

			if !o.Props.CDN.Enabled {
				obj.CacheControl = fields.String("private, max-age=0, no-transform")
			}

			_, err = r.RegisterAppResource(o.App, filePath, obj)
			if err != nil {
				return err
			}

			o.Files = append(o.Files, obj)
		}
	}

	// Add GCR docker image.
	o.Image = &gcp.Image{
		Name:      fields.Sprintf("%s/%s", plugin_util.SanitizeName(pctx.Env().Env()), gcp.GCSProxyImageName),
		Tag:       fields.String(gcp.GCSProxyVersion),
		ProjectID: fields.String(c.ProjectID),
		GCR:       fields.String(gcp.RegionToGCR(c.Region)),
		Source:    fields.String(gcp.GCSProxyDockerImage),
		Pull:      true,
	}

	_, err = r.RegisterPluginResource(CommonName, gcp.GCSProxyImageName, o.Image)
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
		Name:      gcp.IDField(pctx.Env(), o.App.Id),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		Image:     o.Image.ImageName(),
		IsPublic:  fields.Bool(true),
		EnvVars:   fields.Map(envVars),

		MinScale: fields.Int(o.DeployOpts.MinScale),
		MaxScale: fields.Int(o.DeployOpts.MaxScale),
	}

	_, err = r.RegisterAppResource(o.App, "cloud_run", o.CloudRun)
	if err != nil {
		return err
	}

	return nil
}
