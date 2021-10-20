package actions

import (
	"context"
	"fmt"
	"net/url"

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

	appEnvVarsStr map[string]string
	appEnvVars    map[string]map[string]interface{} // type->name->value

	staticApps   map[string]*deploy.StaticApp
	serviceApps  map[string]*deploy.ServiceApp
	databaseDeps map[string]*deploy.DatabaseDep
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
		pluginCtx:     pctx,
		log:           logger,
		apiRegistry:   registry.NewRegistry(),
		registry:      r,
		appIDMap:      make(map[string]*types.App),
		appEnvVarsStr: make(map[string]string),
		appEnvVars:    make(map[string]map[string]interface{}),

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
		verify:           verify,
		destroy:          destroy,
		fullCheck:        fullCheck,
	}, nil
}

func (p *PlanAction) planApps(appPlans []*types.AppPlan) error {
	var (
		staticAppsPlan  []*types.AppPlan
		serviceAppsPlan []*types.AppPlan
	)

	for _, app := range appPlans {
		prefix := app.App.EnvPrefix()
		p.appIDMap[app.App.ID] = app.App

		p.appEnvVarsStr[fmt.Sprintf("%sURL", prefix)] = app.App.URL

		u, err := url.Parse(app.App.URL)
		if err != nil {
			return err
		}

		appEnvVars := map[string]interface{}{
			"url":  fields.String(app.App.URL),
			"host": fields.String(u.Host),
			"path": fields.String(u.Path),
		}

		if _, ok := p.appEnvVars[app.App.Type]; !ok {
			p.appEnvVars[app.App.Type] = map[string]interface{}{
				app.App.Name: appEnvVars,
			}
		} else {
			p.appEnvVars[app.App.Type][app.App.Name] = appEnvVars
		}

		if !app.IsDeploy {
			continue
		}

		switch app.App.Type {
		case TypeStatic:
			staticAppsPlan = append(staticAppsPlan, app)
		case TypeService:
			serviceAppsPlan = append(serviceAppsPlan, app)
		}
	}

	var err error

	// Plan static app deployment.
	p.staticApps, err = p.planStaticAppsDeploy(staticAppsPlan)
	if err != nil {
		return err
	}

	// Plan service app deployment.
	p.serviceApps, err = p.planServiceAppsDeploy(serviceAppsPlan)
	if err != nil {
		return err
	}

	return nil
}

func (p *PlanAction) planDependencies(appPlans []*types.AppPlan, depPlans []*types.DependencyPlan) error {
	allNeeds := make(map[string]map[*types.App]*types.AppNeed)

	for _, d := range appPlans {
		for _, n := range d.App.Needs {
			if _, ok := allNeeds[n.Dependency]; !ok {
				allNeeds[n.Dependency] = make(map[*types.App]*types.AppNeed)
			}

			allNeeds[n.Dependency][d.App] = n
		}
	}

	var (
		databasePlan []*types.DependencyPlan
	)

	for _, dep := range depPlans {
		switch dep.Dependency.Type {
		case DepTypePostgresql, DepTypeMySQL:
			databasePlan = append(databasePlan, dep)
		}
	}

	var err error

	// Plan dependency deployment.
	p.databaseDeps, err = p.planDatabaseDepsDeploy(databasePlan, allNeeds)
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

	// Skip Read to avoid being rate limited. And it shouldn't really be necessary to recheck it.
	err := p.apiRegistry.Load(ctx, apiReg, p.pluginCtx, &registry.Options{
		Read: p.fullCheck,
	})
	if err != nil {
		return err
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

func (p *PlanAction) planAll(ctx context.Context, appPlans []*types.AppPlan, depPlans []*types.DependencyPlan) error {
	err := p.planDependencies(appPlans, depPlans)
	if err != nil {
		return err
	}

	err = p.planApps(appPlans)
	if err != nil {
		return err
	}

	p.loadBalancer = deploy.NewLoadBalancer()

	err = p.loadBalancer.Plan(p.pluginCtx, p.registry, p.staticApps, p.serviceApps, &deploy.LoadBalancerArgs{
		Name:      "load_balancer",
		ProjectID: p.pluginCtx.Settings().ProjectID,
		Region:    p.pluginCtx.Settings().Region,
	})
	if err != nil {
		return err
	}

	// Process registry.
	reg := p.PluginMap["registry"]

	err = p.registry.Load(ctx, reg, p.pluginCtx, &registry.Options{
		Read: p.verify,
	})
	if err != nil {
		return err
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

	for mapURL, appID := range curMapping {
		id := appID.(string)

		state, ok := p.AppStates[id]
		if !ok {
			state = types.NewAppState(p.appIDMap[id])
		}

		u, _ := url.Parse(mapURL)
		domain := u.Hostname()

		ssl := p.loadBalancer.ManagedSSLDomainMap[domain]
		sslStatus := types.SSLStatusUnknown
		sslStatusInfo := ""

		if ssl != nil {
			switch ssl.Status.Current() {
			case "ACTIVE":
				sslStatus = types.SSLStatusOK
			case "PROVISIONING":
				sslStatus = types.SSLStatusProvisioning
			case "PROVISIONING_FAILED", "PROVISIONING_FAILED_PERMANENTLY":
				sslStatus = types.SSLStatusProvisioningFailed
			case "RENEWAL_FAILED":
				sslStatus = types.SSLStatusRenewalFailed
			}

			if v, ok := ssl.DomainStatus.Current()[domain]; ok {
				sslStatusInfo = v.(string)
			}
		}

		state.DNS = &types.DNS{
			IP:            p.loadBalancer.Addresses[0].IP.Current(),
			URL:           mapURL,
			Manual:        true,
			SSLStatus:     sslStatus,
			SSLStatusInfo: sslStatusInfo,
		}

		p.AppStates[id] = state
	}

	return nil
}

func (p *PlanAction) Plan(ctx context.Context, appPlans []*types.AppPlan, depPlans []*types.DependencyPlan) (*types.Plan, error) {
	err := p.enableAPIs(ctx)
	if err != nil {
		return nil, err
	}

	err = p.planAll(ctx, appPlans, depPlans)
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

func (p *PlanAction) Apply(ctx context.Context, appPlans []*types.AppPlan, depPlans []*types.DependencyPlan, cb func(a *types.ApplyAction)) error {
	err := p.enableAPIs(ctx)
	if err != nil {
		return err
	}

	err = p.planAll(ctx, appPlans, depPlans)
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
