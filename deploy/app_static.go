package deploy

import (
	"fmt"
	"mime"
	"path/filepath"

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

	App  *types.App
	Opts *StaticAppOptions
}

type StaticAppArgs struct {
	ProjectID string
	Region    string
}

func NewStaticApp(app *types.App) *StaticApp {
	opts := &StaticAppOptions{}
	if err := opts.Decode(app.Properties); err != nil {
		panic(err)
	}

	return &StaticApp{
		App:  app,
		Opts: opts,
	}
}

type StaticAppOptions struct {
	Build struct {
		Dir string `mapstructure:"dir"`
	} `mapstructure:"build"`

	Routing string `mapstructure:"routing"`

	CDN struct {
		Enabled bool `mapstructure:"enabled"`
	} `mapstructure:"cdn"`
}

func (o *StaticAppOptions) Decode(in interface{}) error {
	return mapstructure.Decode(in, o)
}

func (o *StaticApp) Plan(pctx *config.PluginContext, r *registry.Registry, c *StaticAppArgs, verify bool) error {
	buildDir := filepath.Join(o.App.Dir, o.Opts.Build.Dir)

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

	err := r.Register(o.Bucket, o.App.ID, "bucket")
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

		if !o.Opts.CDN.Enabled {
			obj.CacheControl = fields.String("private, max-age=0, no-transform")
		}

		err = r.Register(obj, o.App.ID, filePath)
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

	err = r.Register(o.Image, CommonName, gcp.GCSProxyImageName)
	if err != nil {
		return err
	}

	envVars := map[string]fields.Field{
		"GCS_BUCKET":  o.Bucket.Name,
		"FORCE_HTTPS": fields.String("1"),
		"ROUTING":     fields.String(o.Opts.Routing),
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
