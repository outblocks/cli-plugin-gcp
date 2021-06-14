package deploy

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/deploy/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/types"
	pluginutil "github.com/outblocks/outblocks-plugin-go/util"
)

type StaticApp struct {
	Bucket        *gcp.Bucket   `json:"bucket"`
	ProxyImage    *gcp.Image    `json:"proxy_image" mapstructure:"proxy_image"`
	ProxyCloudRun *gcp.CloudRun `json:"proxy_cloud_run" mapstructure:"proxy_cloud_run"`

	// TODO: support for cleanup of not needed stuff
	Other map[string]interface{} `json:"-" mapstructure:",remain"`

	Planned *StaticAppPlan `json:"-" mapstructure:"-"`
}

type StaticAppPlan struct {
	Bucket        *gcp.BucketCreate
	ProxyImage    *gcp.ImageCreate
	ProxyCloudRun *gcp.CloudRunCreate
}

type StaticAppCreate struct {
	ProjectID string
	Region    string
}

func NewStaticApp() *StaticApp {
	return &StaticApp{
		ProxyImage:    &gcp.Image{},
		Bucket:        &gcp.Bucket{},
		ProxyCloudRun: &gcp.CloudRun{},

		Planned: &StaticAppPlan{},
	}
}

func (o *StaticApp) Decode(in interface{}) error {
	return mapstructure.Decode(in, o)
}

func (o *StaticApp) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		StaticApp
		Type string `json:"type"`
	}{
		StaticApp: *o,
		Type:      "static_app",
	})
}

type StaticAppOptions struct {
	Routing string `mapstructure:"routing"`
}

func (o *StaticAppOptions) IsReactRouting() bool {
	return o.Routing == "" || strings.EqualFold(o.Routing, "react")
}

func (o *StaticAppOptions) Decode(in interface{}) error {
	return mapstructure.Decode(in, o)
}

func (o *StaticApp) Plan(pctx *config.PluginContext, app *types.App, c *StaticAppCreate, verify bool) (*types.AppPlanActions, error) {
	plan := types.NewAppPlanActions(app)

	opts := &StaticAppOptions{}
	if err := opts.Decode(app.Properties); err != nil {
		return nil, err
	}

	if c != nil {
		build := app.Properties["build"].(map[string]interface{})
		buildDir := filepath.Join(app.Path, build["dir"].(string))

		buildPath, ok := pluginutil.CheckDir(buildDir)
		if !ok {
			return nil, fmt.Errorf("app '%s' build dir '%s' does not exist", app.Name, buildDir)
		}

		o.Planned.Bucket = &gcp.BucketCreate{
			Name:       gcp.ID(pctx.Env().ProjectName(), c.ProjectID, app.ID),
			ProjectID:  c.ProjectID,
			Location:   c.Region,
			Versioning: false,
			IsPublic:   true,
			Path:       buildPath,
		}

		o.Planned.ProxyImage = &gcp.ImageCreate{
			Name:      gcp.GCSProxyImageName,
			ProjectID: c.ProjectID,
			Source:    gcp.GCSProxyDockerImage,
			GCR:       gcp.RegionToGCR(c.Region),
		}

		envVars := map[string]string{
			"GCS_BUCKET": o.Planned.Bucket.Name,
		}

		if opts.IsReactRouting() {
			envVars["ERROR404"] = "index.html"
			envVars["ERROR404_CODE"] = "200"
		}

		o.Planned.ProxyCloudRun = &gcp.CloudRunCreate{
			Name:      gcp.ID(pctx.Env().ProjectName(), c.ProjectID, app.ID),
			Image:     o.Planned.ProxyImage.ImageName(),
			ProjectID: c.ProjectID,
			Region:    c.Region,
			IsPublic:  true,
			Options: &gcp.RunServiceOptions{
				EnvVars: envVars,
			},
		}
	}

	// Plan Bucket.
	err := plan.PlanObject(pctx, o, "Bucket", o.Planned.Bucket, verify)
	if err != nil {
		return nil, err
	}

	// Plan GCR Proxy Image.
	err = plan.PlanObject(pctx, o, "ProxyImage", o.Planned.ProxyImage, verify)
	if err != nil {
		return nil, err
	}

	// Plan Cloud Run.
	err = plan.PlanObject(pctx, o, "ProxyCloudRun", o.Planned.ProxyCloudRun, verify)
	if err != nil {
		return nil, err
	}

	return plan, nil
}
