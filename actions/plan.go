package actions

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
)

type PlanAction struct {
	pluginCtx   *config.PluginContext
	log         log.Logger
	apiRegistry *registry.Registry
	registry    *registry.Registry
	appIDMap    map[string]*types.App

	staticApps   map[string]*deploy.StaticApp
	loadBalancer *deploy.LoadBalancer

	PluginMap                  types.PluginStateMap
	AppStates                  map[string]*types.AppState
	DependencyStates           map[string]*types.DependencyState
	verify, destroy, fullCheck bool
}

func NewPlan(pctx *config.PluginContext, logger log.Logger, state types.PluginStateMap, appStates map[string]*types.AppState, depStates map[string]*types.DependencyState, verify, destroy, fullCheck bool) (*PlanAction, error) {
	if state == nil {
		state = make(types.PluginStateMap)
	}

	if appStates == nil {
		appStates = make(map[string]*types.AppState)
	}

	if depStates == nil {
		depStates = make(map[string]*types.DependencyState)
	}

	r := registry.NewRegistry()

	for _, t := range gcp.Types {
		err := r.RegisterType(t)
		if err != nil {
			return nil, err
		}
	}

	return &PlanAction{
		pluginCtx:   pctx,
		log:         logger,
		apiRegistry: registry.NewRegistry(),
		registry:    r,
		appIDMap:    make(map[string]*types.App),

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
		verify:           verify,
		destroy:          destroy,
		fullCheck:        fullCheck,
	}, nil
}

func (p *PlanAction) planStaticAppDeploy(appPlan *types.AppPlan) (*deploy.StaticApp, error) {
	appDeploy := deploy.NewStaticApp(appPlan.App)
	pctx := p.pluginCtx

	err := appDeploy.Plan(pctx, p.registry, appPlan.App, &deploy.StaticAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Path:      appPlan.Path,
	}, p.verify)

	return appDeploy, err
}

func (p *PlanAction) planStaticAppsDeploy(appPlans []*types.AppPlan) (ret map[string]*deploy.StaticApp, err error) {
	ret = make(map[string]*deploy.StaticApp, len(appPlans))

	for _, plan := range appPlans {
		app, err := p.planStaticAppDeploy(plan)
		if err != nil {
			return ret, err
		}

		ret[app.App.ID] = app
	}

	return ret, nil
}

func (p *PlanAction) planApps(appPlans []*types.AppPlan) error {
	var (
		staticAppsPlan []*types.AppPlan
	)

	for _, app := range appPlans {
		p.appIDMap[app.App.ID] = app.App

		if !app.IsDeploy {
			continue
		}

		if app.App.Type == TypeStatic {
			staticAppsPlan = append(staticAppsPlan, app)
		}
	}

	var err error

	// Plan static app deployment.
	p.staticApps, err = p.planStaticAppsDeploy(staticAppsPlan)
	if err != nil {
		return err
	}

	return nil
}

func (p *PlanAction) enableAPIs(ctx context.Context) error {
	// Process API registry.
	for _, api := range gcp.APISRequired {
		s := &gcp.APIService{Name: fields.String(api)}

		err := p.apiRegistry.Register(s, deploy.APIName, api)
		if err != nil {
			return err
		}
	}

	apiReg := p.PluginMap["api_registry"]

	err := p.apiRegistry.Load(ctx, apiReg)
	if err != nil {
		return err
	}

	// Skip Read to avoid being rate limited. And it shouldn't really be necessary to recheck it.
	if p.fullCheck {
		err = p.apiRegistry.Read(ctx, p.pluginCtx)
		if err != nil {
			return err
		}
	}

	diff, err := p.apiRegistry.Diff(ctx, false)
	if err != nil {
		return err
	}

	if len(diff) != 0 {
		p.log.Infoln("Enabling required Project Service APIs...")
	}

	err = p.apiRegistry.Apply(ctx, p.pluginCtx, diff, nil)
	if err != nil {
		return err
	}

	data, err := p.apiRegistry.Dump()
	if err != nil {
		return err
	}

	p.PluginMap["api_registry"] = data

	return nil
}

func (p *PlanAction) planAll(ctx context.Context, appPlans []*types.AppPlan) error {
	err := p.planApps(appPlans)
	if err != nil {
		return err
	}

	p.loadBalancer = deploy.NewLoadBalancer()

	err = p.loadBalancer.Plan(p.pluginCtx, p.registry, p.staticApps, &deploy.LoadBalancerArgs{
		Name:      "load_balancer",
		ProjectID: p.pluginCtx.Settings().ProjectID,
		Region:    p.pluginCtx.Settings().Region,
	}, p.verify)
	if err != nil {
		return err
	}

	// Process registry.
	reg := p.PluginMap["registry"]

	err = p.registry.Load(ctx, reg)
	if err != nil {
		return err
	}

	if p.verify {
		err = p.registry.Read(ctx, p.pluginCtx)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *PlanAction) diff(ctx context.Context) (appPlanActions []*types.AppPlanActions, pluginPlanActions []*types.PluginPlanActions, err error) {
	// Process diffs.
	diff, err := p.registry.Diff(ctx, p.destroy)
	if err != nil {
		return nil, nil, err
	}

	appPlanMap := make(map[string]*types.AppPlanActions)
	pluginPlanMap := make(map[string]*types.PluginPlanActions)

	for _, d := range diff {
		ns := d.Object.Namespace

		if app, ok := p.appIDMap[ns]; ok {
			appPlan, ok := appPlanMap[ns]
			if !ok {
				appPlan = types.NewAppPlanActions(app)
				appPlanMap[ns] = appPlan
				appPlanActions = append(appPlanActions, appPlan)
			}

			appPlan.Actions = append(appPlan.Actions, d.ToPlanAction())
		} else {
			pluginPlan, ok := pluginPlanMap[ns]
			if !ok {
				pluginPlan = types.NewPluginPlanActions(d.Object.Namespace)
				pluginPlanMap[ns] = pluginPlan
				pluginPlanActions = append(pluginPlanActions, pluginPlan)
			}

			pluginPlan.Actions = append(pluginPlan.Actions, d.ToPlanAction())
		}
	}

	return appPlanActions, pluginPlanActions, nil
}

func (p *PlanAction) save() error {
	data, err := p.registry.Dump()
	if err != nil {
		return err
	}

	p.PluginMap["registry"] = data

	if p.destroy {
		return nil
	}

	curMapping := p.loadBalancer.URLMaps[0].AppMapping.Current()

	for url, appID := range curMapping {
		id := appID.(string)

		state, ok := p.AppStates[id]
		if !ok {
			app := p.staticApps[id]
			state = types.NewAppState(app.App)
		}

		state.DNS = &types.DNS{
			IP:     p.loadBalancer.Addresses[0].IP.Current(),
			URL:    "https://" + url,
			Manual: true,
		}

		p.AppStates[id] = state
	}

	return nil
}

func (p *PlanAction) Plan(ctx context.Context, appPlans []*types.AppPlan) (*types.Plan, error) {
	err := p.enableAPIs(ctx)
	if err != nil {
		return nil, err
	}

	err = p.planAll(ctx, appPlans)
	if err != nil {
		return nil, err
	}

	appPlanActions, pluginPlanActions, err := p.diff(ctx)
	if err != nil {
		return nil, err
	}

	err = p.save()
	if err != nil {
		return nil, err
	}

	return &types.Plan{
		Apps:   appPlanActions,
		Plugin: pluginPlanActions,
	}, nil
}

func (p *PlanAction) Apply(ctx context.Context, appPlans []*types.AppPlan, cb func(a *types.ApplyAction)) error {
	err := p.enableAPIs(ctx)
	if err != nil {
		return err
	}

	err = p.planAll(ctx, appPlans)
	if err != nil {
		return err
	}

	diff, err := p.registry.Diff(ctx, p.destroy)
	if err != nil {
		return err
	}

	err = p.registry.Apply(ctx, p.pluginCtx, diff, cb)
	saveErr := p.save()

	if err != nil {
		return err
	}

	return saveErr
}
