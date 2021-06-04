package actions

import (
	"context"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/types"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

type PlanAction struct {
	ctx        context.Context
	storageCli *storage.Client
	computeCli *compute.Service
	gcred      *google.Credentials
	settings   *Settings
	log        log.Logger
	env        env.Enver

	common *deploy.Common

	PluginMap        types.PluginStateMap
	AppStates        map[string]*types.AppState
	DependencyStates map[string]*types.DependencyState
	verify           bool
}

func NewPlan(ctx context.Context, gcred *google.Credentials, settings *Settings, logger log.Logger, enver env.Enver, state types.PluginStateMap, appStates map[string]*types.AppState, depStates map[string]*types.DependencyState, verify bool) (*PlanAction, error) {
	storageCli, err := config.NewStorageClient(ctx, gcred)
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

	common := deploy.NewCommon()

	computeCli, err := config.NewGCPComputeClient(ctx, gcred)
	if err != nil {
		return nil, err
	}

	err = common.Decode(state[PluginCommonState])
	if err != nil {
		return nil, err
	}

	return &PlanAction{
		ctx:        ctx,
		storageCli: storageCli,
		computeCli: computeCli,
		gcred:      gcred,
		settings:   settings,
		log:        logger,
		env:        enver,

		common: common,

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
		verify:           verify,
	}, nil
}

func (p *PlanAction) handleStaticAppDeploy(state *types.AppState, app *types.App) (*deploy.StaticApp, *types.AppPlanActions, error) {
	appDeploy := deploy.NewStaticApp()
	if err := appDeploy.Decode(state.DeployState[PluginName]); err != nil {
		return nil, nil, err
	}

	actions, err := appDeploy.Plan(p.ctx, p.storageCli, p.gcred, app, p.env, &deploy.StaticAppCreate{
		ProjectID: p.settings.ProjectID,
		Region:    p.settings.Region,
	}, p.verify)
	if err != nil {
		return nil, nil, err
	}

	return appDeploy, actions, nil
}

func (p *PlanAction) handleStaticAppsDeploy(appPlans []*types.AppPlan) (apps []*deploy.StaticApp, appPlan []*types.AppPlanActions, err error) {
	for _, plan := range appPlans {
		state := p.AppStates[plan.App.ID]
		if state == nil {
			state = types.NewAppState()
			p.AppStates[plan.App.ID] = state
		}

		sa, aa, e := p.handleStaticAppDeploy(state, plan.App)
		if e != nil {
			return nil, nil, e
		}

		if p.verify {
			state.DeployState[PluginName] = sa
			state.App = plan.App
		}

		if aa != nil && len(aa.Actions) > 0 {
			appPlan = append(appPlan, aa)
		}

		apps = append(apps, sa)
	}

	return
}

func (p *PlanAction) handleCleanupApp(state *types.AppState) (appPlan *types.AppPlanActions, err error) {
	deployState := state.DeployState[PluginName]
	if deployState == nil {
		return nil, nil
	}

	app, err := deploy.DetectAppType(deployState)
	if err != nil {
		return nil, err
	}

	switch appDeploy := app.(type) { // nolint: gocritic // - more app types will be supported
	case *deploy.StaticApp:
		appPlan, err = appDeploy.Plan(p.ctx, p.storageCli, p.gcred, state.App, p.env, nil, p.verify)
		if err != nil {
			return nil, err
		}
	}

	return appPlan, nil
}

func (p *PlanAction) planDeployCommon(static []*deploy.StaticApp, staticPlan []*types.AppPlan) (*types.PluginPlanActions, error) {
	actions, err := p.common.Plan(p.ctx, p.computeCli, p.env, &deploy.CommonCreate{
		Name:      PluginCommonState,
		ProjectID: p.settings.ProjectID,
		Region:    p.settings.Region,
	}, static, staticPlan,
		p.verify)
	if err != nil {
		return nil, err
	}

	if p.verify {
		p.PluginMap[PluginCommonState] = p.common
	}

	if len(actions) == 0 {
		return nil, nil
	}

	plan := types.NewPluginPlanActions(PluginCommonState)
	plan.Actions = actions

	return plan, nil
}

func (p *PlanAction) PlanDeploy(apps []*types.AppPlan) (*types.Plan, error) {
	var (
		staticAppsPlan []*types.AppPlan
		pluginPlan     []*types.PluginPlanActions
	)

	appIDs := make(map[string]struct{})

	for _, app := range apps {
		appIDs[app.App.ID] = struct{}{}

		if !app.IsDeploy {
			continue
		}

		if app.App.Type == TypeStatic {
			staticAppsPlan = append(staticAppsPlan, app)
		}
	}

	var (
		appPlan []*types.AppPlanActions
	)

	// Plan static app deployment.
	staticApps, staticAppDeployPlan, err := p.handleStaticAppsDeploy(staticAppsPlan)
	if err != nil {
		return nil, err
	}

	appPlan = append(appPlan, staticAppDeployPlan...)

	// Plan cleanup.
	for appID, appState := range p.AppStates {
		if _, ok := appIDs[appID]; !ok {
			cleanupPlan, err := p.handleCleanupApp(appState)
			if err != nil {
				return nil, err
			}

			if cleanupPlan != nil {
				appPlan = append(appPlan, cleanupPlan)
			}
		}
	}

	// Plan common part.
	pluginAction, err := p.planDeployCommon(staticApps, staticAppsPlan)
	if err != nil {
		return nil, err
	}

	if pluginAction != nil {
		pluginPlan = append(pluginPlan, pluginAction)
	}

	return &types.Plan{
		Apps:   appPlan,
		Plugin: pluginPlan,
	}, nil
}
