package deploy

import (
	"fmt"
	"mime"
	"path/filepath"

	validation "github.com/go-ozzo/ozzo-validation/v4"
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
	Skip       bool
	Props      *types.StaticAppProperties
	DeployOpts *StaticAppDeployOptions
}

type StaticAppArgs struct {
	ProjectID string
	Region    string
}

type StaticAppDeployOptions struct {
	types.StaticAppDeployOptions
}

func NewStaticAppDeployOptions(in map[string]interface{}) (*StaticAppDeployOptions, error) {
	o := &StaticAppDeployOptions{}

	err := plugin_util.MapstructureJSONDecode(in, o)
	if err != nil {
		return nil, fmt.Errorf("error decoding static app deploy options: %w", err)
	}

	// Manual defaults.
	if o.MaxScale == 0 {
		o.MaxScale = 100
	}

	if o.Timeout == 0 {
		o.Timeout = 300
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
		Skip:       plan.Skip,
		Props:      opts,
		DeployOpts: deployOpts,
	}, nil
}

func (o *StaticApp) ID(pctx *config.PluginContext) string {
	return gcp.ID(pctx.Env(), o.App.Id)
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
	if !o.Skip {
		buildPath, ok := plugin_util.CheckDir(buildDir)
		if !ok {
			return fmt.Errorf("%s app '%s' build dir '%s' does not exist", o.App.Type, o.App.Name, buildDir)
		}

		files, err := findFiles(buildPath, o.DeployOpts.Patterns)
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
		Name:      fields.String(gcp.ImageID(pctx.Env(), gcp.GCSProxyImageName)),
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
		"GCS_BUCKET": o.Bucket.Name,
		"ROUTING":    fields.String(o.Props.Routing),
	}

	if o.Props.RemoveTrailingSlash != nil {
		val := "0"
		if *o.Props.RemoveTrailingSlash {
			val = "1"
		}

		envVars["REMOVE_TRAILING_SLASH"] = fields.String(val)
	}

	if o.Props.BasicAuth != nil && len(o.Props.BasicAuth.Users) != 0 {
		envVars["BASIC_AUTH_REALM"] = fields.String(o.Props.BasicAuth.Realm)

		for u, p := range o.Props.BasicAuth.Users {
			envVars[fmt.Sprintf("ACCOUNT_%s", u)] = fields.String(p)
		}
	}

	// Add cloud run service.
	o.CloudRun = &gcp.CloudRun{
		Name:      fields.String(o.ID(pctx)),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		Image:     o.Image.ImageName(),
		IsPublic:  fields.Bool(true),
		EnvVars:   fields.Map(envVars),

		MinScale:       fields.Int(o.DeployOpts.MinScale),
		MaxScale:       fields.Int(o.DeployOpts.MaxScale),
		TimeoutSeconds: fields.Int(o.DeployOpts.Timeout),
	}

	_, err = r.RegisterAppResource(o.App, "cloud_run", o.CloudRun)
	if err != nil {
		return err
	}

	return nil
}
