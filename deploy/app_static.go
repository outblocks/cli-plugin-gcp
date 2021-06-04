package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/types"
	pluginutil "github.com/outblocks/outblocks-plugin-go/util"
	"golang.org/x/oauth2/google"
)

const (
	StaticProxyImage   = "proxy_image"
	StaticCloudRun     = "proxy_cloud_run"
	StaticBucketObject = "bucket"
)

type StaticApp struct {
	Name          string       `json:"string"`
	Bucket        *GCPBucket   `json:"bucket"`
	ProxyImage    *GCPImage    `json:"proxy_image" mapstructure:"proxy_image"`
	ProxyCloudRun *GCPCloudRun `json:"proxy_cloud_run" mapstructure:"proxy_cloud_run"`

	// TODO: support for cleanup of not needed stuff
	Other map[string]interface{} `json:"-" mapstructure:",remain"`
}

func NewStaticApp() *StaticApp {
	return &StaticApp{
		ProxyImage:    &GCPImage{},
		Bucket:        &GCPBucket{},
		ProxyCloudRun: &GCPCloudRun{},
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

type StaticAppCreate struct {
	ProjectID string
	Region    string
}

func (o *StaticApp) Plan(ctx context.Context, storageCli *storage.Client, gcred *google.Credentials, app *types.App, e env.Enver, c *StaticAppCreate, verify bool) (*types.AppPlanActions, error) {
	var (
		bucketCreate     *GCPBucketCreate
		proxyImageCreate *GCPImageCreate
		cloudRunCreate   *GCPCloudRunCreate
	)

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

		bucketCreate = &GCPBucketCreate{
			Name:       ID(e.ProjectName(), c.ProjectID, app.ID),
			ProjectID:  c.ProjectID,
			Location:   c.Region,
			Versioning: false,
			IsPublic:   true,
			Path:       buildPath,
		}

		proxyImageCreate = &GCPImageCreate{
			Name:      GCSProxyImageName,
			ProjectID: c.ProjectID,
			Source:    GCSProxyDockerImage,
			GCR:       RegionToGCR(c.Region),
		}

		envVars := map[string]string{
			"GCS_BUCKET": o.Bucket.Name,
		}

		if opts.IsReactRouting() {
			envVars["ERROR404"] = "index.html"
			envVars["ERROR404_CODE"] = "200"
		}

		cloudRunCreate = &GCPCloudRunCreate{
			Name:      ID(e.ProjectName(), c.ProjectID, app.ID),
			Image:     o.ProxyImage.ImageName(),
			ProjectID: c.ProjectID,
			Region:    c.Region,
			IsPublic:  true,
			Options: &RunServiceOptions{
				EnvVars: envVars,
			},
		}
	}

	// Plan Bucket.
	err := util.PlanObject(plan.Actions, StaticBucketObject, o.Bucket.planner(ctx, storageCli, bucketCreate, verify))
	if err != nil {
		return nil, err
	}

	// Plan GCR Proxy Image.
	err = util.PlanObject(plan.Actions, StaticProxyImage, o.ProxyImage.planner(ctx, gcred, proxyImageCreate, verify))
	if err != nil {
		return nil, err
	}

	// Plan Cloud Run.
	err = util.PlanObject(plan.Actions, StaticCloudRun, o.ProxyCloudRun.planner(ctx, gcred, cloudRunCreate, verify))
	if err != nil {
		return nil, err
	}

	return plan, nil
}

func (o *StaticApp) Apply(ctx context.Context, storageCli *storage.Client, dockerCli *dockerclient.Client, gcred *google.Credentials, actions map[string]*types.PlanAction, callback func(obj, desc string, progress, total int)) error {
	// Apply Bucket.
	err := util.ApplyObject(actions, StaticBucketObject, callback, o.Bucket.applier(ctx, storageCli))
	if err != nil {
		return err
	}

	// Apply GCR.
	err = util.ApplyObject(actions, StaticProxyImage, callback, o.ProxyImage.applier(ctx, dockerCli, gcred))
	if err != nil {
		return err
	}

	// Apply Cloud Run.
	err = util.ApplyObject(actions, StaticCloudRun, callback, o.ProxyCloudRun.applier(ctx, gcred))

	return err
}
