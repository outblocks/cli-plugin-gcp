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

	PluginMap        types.PluginStateMap
	AppStates        map[string]*types.AppState
	DependencyStates map[string]*types.DependencyState
	verify           bool
}

func NewPlan(ctx context.Context, gcred *google.Credentials, settings *Settings, logger log.Logger, enver env.Enver, state types.PluginStateMap, appStates map[string]*types.AppState, depStates map[string]*types.DependencyState, verify bool) (*PlanAction, error) {
	storageCli, err := config.NewStorageCli(ctx, gcred)
	if err != nil {
		return nil, err
	}

	if state == nil {
		state = make(types.PluginStateMap)
	}

	if appStates == nil {
		appStates = make(map[string]*types.AppState)
	}

	if depStates == nil {
		depStates = make(map[string]*types.DependencyState)
	}

	return &PlanAction{
		ctx:        ctx,
		storageCli: storageCli,
		gcred:      gcred,
		settings:   settings,
		log:        logger,
		env:        enver,

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
		verify:           verify,
	}, nil
}

func (p *PlanAction) handleStaticAppDeploy(app *types.App) (*types.AppPlanActions, error) {
	build := app.Properties["build"].(map[string]interface{})
	buildDir := filepath.Join(app.Path, build["dir"].(string))

	buildPath, ok := util.CheckDir(buildDir)
	if !ok {
		return nil, fmt.Errorf("app '%s' build dir '%s' does not exist", app.Name, buildDir)
	}

	plan := types.NewAppPlanActions(app)
	deployState := deploy.NewStaticApp()

	state := p.AppStates[app.ID]
	if state == nil {
		state = types.NewAppState()
		p.AppStates[app.ID] = state
	}

	if err := deployState.Decode(state.DeployState[PluginName]); err != nil {
		return nil, err
	}

	// Plan Bucket.
	err := p.planObject(deployState.Bucket, plan.Actions, StaticBucketObject,
		&deploy.GCPBucketCreate{
			Name:       deploy.BucketName(p.env.ProjectName(), p.settings.ProjectID, app.ID),
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
	err = p.planObject(deployState.ProxyImage, plan.Actions, StaticProxyImage,
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

	if p.verify {
		state.DeployState[PluginName] = deployState
	}

	return plan, nil
}

func (p *PlanAction) planObject(i interface{}, actions map[string]*types.PlanAction, obj string, c interface{}) error {
	var (
		action *types.PlanAction
		err    error
	)

	switch o := i.(type) {
	case *deploy.GCPBucket:
		action, err = o.Plan(p.ctx, p.storageCli, c.(*deploy.GCPBucketCreate), p.verify)
	case *deploy.GCPImage:
		action, err = o.Plan(p.ctx, p.gcred, c.(*deploy.GCPImageCreate), p.verify)
	}

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
		aa, e := p.handleStaticAppDeploy(app.App)
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
