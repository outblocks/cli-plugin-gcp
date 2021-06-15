package actions

import (
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/types"
)

type PlanAction struct {
	pluginCtx *config.PluginContext
	log       log.Logger

	PluginMap        types.PluginStateMap
	AppStates        map[string]*types.AppState
	DependencyStates map[string]*types.DependencyState
	verify           bool
}

func NewPlan(pctx *config.PluginContext, logger log.Logger, state types.PluginStateMap, appStates map[string]*types.AppState, depStates map[string]*types.DependencyState, verify bool) (*PlanAction, error) {
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
		pluginCtx: pctx,
		log:       logger,

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
		verify:           verify,
	}, nil
}

func (p *PlanAction) handleStaticAppDeploy(state *types.AppState, appPlan *types.AppPlan) (*deploy.StaticApp, *types.AppPlanActions, error) {
	appDeploy := deploy.NewStaticApp()
	if err := appDeploy.Decode(state.DeployState[PluginName]); err != nil {
		return nil, nil, err
	}

	actions, err := appDeploy.Plan(p.pluginCtx, appPlan.App, &deploy.StaticAppCreate{
		ProjectID: p.pluginCtx.Settings().ProjectID,
		Region:    p.pluginCtx.Settings().Region,
		Path:      appPlan.Path,
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

		sa, aa, e := p.handleStaticAppDeploy(state, plan)
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
		appPlan, err = appDeploy.Plan(p.pluginCtx, state.App, nil, p.verify)
		if err != nil {
			return nil, err
		}
	}

	return appPlan, nil
}

func (p *PlanAction) planDeployLB(static []*deploy.StaticApp, staticPlan []*types.AppPlan) (*types.PluginPlanActions, error) {
	lb := deploy.NewLoadBalancer()

	err := lb.Decode(p.PluginMap[PluginLBState])
	if err != nil {
		return nil, err
	}

	plan := types.NewPluginPlanActions(PluginLBState)

	err = lb.Plan(p.pluginCtx, plan, &deploy.LoadBalancerCreate{
		Name:      PluginLBState,
		ProjectID: p.pluginCtx.Settings().ProjectID,
		Region:    p.pluginCtx.Settings().Region,
	}, static, staticPlan,
		p.verify)
	if err != nil {
		return nil, err
	}

	if p.verify {
		p.PluginMap[PluginLBState] = lb
	}

	if len(plan.Actions) == 0 {
		return nil, nil
	}

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

	// Plan load balancer.
	pluginAction, err := p.planDeployLB(staticApps, staticAppsPlan)
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
