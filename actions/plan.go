package actions

import (
	"context"
	"fmt"
	"path/filepath"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/cli-plugin-gcp/statetypes/deploy"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/types"
	"github.com/outblocks/outblocks-plugin-go/util"
	"golang.org/x/oauth2/google"
)

type PlanAction struct {
	ctx        context.Context
	storageCli *storage.Client
	gcred      *google.Credentials
	settings   *Settings
	log        log.Logger
	env        env.Enver

	pluginState types.PluginStateMap
	verify      bool
}

func NewPlan(ctx context.Context, gcred *google.Credentials, settings *Settings, logger log.Logger, enver env.Enver, pluginState types.PluginStateMap, verify bool) (*PlanAction, error) {
	storageCli, err := config.NewStorageCli(ctx, gcred)
	if err != nil {
		return nil, err
	}

	return &PlanAction{
		ctx:         ctx,
		storageCli:  storageCli,
		gcred:       gcred,
		settings:    settings,
		log:         logger,
		env:         enver,
		pluginState: pluginState,
		verify:      verify,
	}, nil
}

func (p *PlanAction) handleStaticAppDeploy(app *types.App, state *types.AppState) (*types.AppPlanActions, error) {
	build := app.Properties["build"].(map[string]interface{})
	buildDir := filepath.Join(app.Path, build["dir"].(string))

	buildPath, ok := util.CheckDir(buildDir)
	if !ok {
		return nil, fmt.Errorf("app '%s' build dir '%s' does not exist", app.Name, buildDir)
	}

	plan := types.NewAppPlanActions(app)

	var deployState deploy.StaticApp
	if state != nil {
		if err := deployState.Decode(state.DeployState[PluginName]); err != nil {
			return nil, err
		}
	}

	// Plan Bucket.
	if deployState.Bucket == nil {
		deployState.Bucket = &deploy.GCPBucket{}
	}

	err := p.planGCPBucket(deployState.Bucket, plan.Actions, StaticBucketObject,
		&deploy.GCPBucketCreate{
			Name:       deploy.BucketName(p.env.ProjectName(), p.settings.ProjectID, app.Name),
			ProjectID:  p.settings.ProjectID,
			Location:   p.settings.Region,
			Versioning: false,
			IsPublic:   true,
			Path:       buildPath,
		})
	if err != nil {
		return nil, err
	}

	// Plan GCR Proxy Image.
	if deployState.ProxyImage == nil {
		deployState.ProxyImage = &deploy.GCPImage{}
	}

	err = p.planGCPImage(deployState.ProxyImage, plan.Actions, StaticProxyImage,
		&deploy.GCPImageCreate{
			Name:      deploy.GCSProxyImageName,
			ProjectID: p.settings.ProjectID,
			Source:    deploy.GCSProxyDockerImage,
			GCR:       deploy.RegionToGCR(p.settings.Region),
		})
	if err != nil {
		return nil, err
	}

	// TODO: plan cloud run and LB

	return plan, nil
}

func (p *PlanAction) planGCPBucket(o *deploy.GCPBucket, actions map[string]*types.PlanAction, obj string, c *deploy.GCPBucketCreate) error {
	action, err := o.Plan(p.ctx, p.storageCli, c, p.verify)
	if err != nil {
		return err
	}

	if action != nil {
		actions[obj] = action
	}

	return nil
}

func (p *PlanAction) planGCPImage(o *deploy.GCPImage, actions map[string]*types.PlanAction, obj string, c *deploy.GCPImageCreate) error {
	action, err := o.Plan(p.ctx, p.gcred, c, p.verify)
	if err != nil {
		return err
	}

	if action != nil {
		actions[obj] = action
	}

	return nil
}

func (p *PlanAction) handleStaticAppsDeploy(apps []*types.AppPlan) (appPlan []*types.AppPlanActions, err error) {
	for _, app := range apps {
		aa, e := p.handleStaticAppDeploy(app.App, app.State)
		if e != nil {
			return nil, e
		}

		if aa != nil && len(aa.Actions) > 0 {
			appPlan = append(appPlan, aa)
		}
	}

	return
}

func (p *PlanAction) PlanDeploy(apps []*types.AppPlan) (*types.Plan, error) {
	var (
		staticApps []*types.AppPlan
	)

	for _, app := range apps {
		if !app.IsDeploy {
			continue
		}

		if app.App.Type == TypeStatic {
			staticApps = append(staticApps, app)
		}
	}

	var (
		appPlan []*types.AppPlanActions
	)

	// Plan static app deployment.
	staticAppDeployPlan, err := p.handleStaticAppsDeploy(staticApps)
	if err != nil {
		return nil, err
	}

	appPlan = append(appPlan, staticAppDeployPlan...)

	return &types.Plan{
		Apps: appPlan,
	}, nil
}
