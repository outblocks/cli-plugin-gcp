package deploy

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
	pluginutil "github.com/outblocks/outblocks-plugin-go/util"
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
	Path      string
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

func (o *StaticAppOptions) IsReactRouting() bool {
	return o.Routing == "" || strings.EqualFold(o.Routing, "react")
}

func (o *StaticAppOptions) Decode(in interface{}) error {
	return mapstructure.Decode(in, o)
}

func (o *StaticApp) Plan(pctx *config.PluginContext, r *registry.Registry, app *types.App, c *StaticAppArgs, verify bool) error {
	buildDir := filepath.Join(c.Path, o.Opts.Build.Dir)

	buildPath, ok := pluginutil.CheckDir(buildDir)
	if !ok {
		return fmt.Errorf("app '%s' build dir '%s' does not exist", app.Name, buildDir)
	}

	// Add bucket.
	o.Bucket = &gcp.Bucket{
		Name:       fields.String(gcp.ID(pctx.Env().ProjectName(), c.ProjectID, app.ID)),
		Location:   fields.String(c.Region),
		ProjectID:  fields.String(c.ProjectID),
		Versioning: fields.Bool(false),
	}

	err := r.Register(o.Bucket, app.ID, "bucket")
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

		err = r.Register(obj, app.ID, filePath)
		if err != nil {
			return err
		}

		o.Files = append(o.Files, obj)
	}

	// Add GCR docker image.
	o.Image = &gcp.Image{
		Name:      fields.String(gcp.GCSProxyImageName),
		ProjectID: fields.String(c.ProjectID),
		GCR:       fields.String(gcp.RegionToGCR(c.Region)),
		Source:    fields.String(gcp.GCSProxyDockerImage),
	}

	err = r.Register(o.Image, CommonName, gcp.GCSProxyImageName)
	if err != nil {
		return err
	}

	envVars := map[string]fields.Field{
		"GCS_BUCKET":       o.Bucket.Name,
		"REWRITE_TO_HTTPS": fields.String("1"),
	}

	if o.Opts.IsReactRouting() {
		envVars["ERROR404"] = fields.String("index.html")
		envVars["ERROR404_CODE"] = fields.String("200")
	}

	// Add cloud run service.
	o.CloudRun = &gcp.CloudRun{
		Name:      fields.String(gcp.ID(pctx.Env().ProjectName(), c.ProjectID, app.ID)),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		Image:     o.Image.ImageName(),
		IsPublic:  fields.Bool(true),
		EnvVars:   fields.Map(envVars),
	}

	err = r.Register(o.CloudRun, app.ID, "cloud_run")
	if err != nil {
		return err
	}

	return nil
}
