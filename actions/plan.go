package actions

import (
	"context"
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
	pluginCtx      *config.PluginContext
	log            log.Logger
	apiRegistry    *registry.Registry
	registry       *registry.Registry
	appIDMap       map[string]*types.App
	appDeployIDMap map[string]interface{}
	appEnvVars     map[string]map[string]interface{} // type->name->value

	depIDMap       map[string]*types.Dependency
	depDeployIDMap map[string]interface{}

	staticApps   map[string]*deploy.StaticApp
	serviceApps  map[string]*deploy.ServiceApp
	databaseDeps map[string]*deploy.DatabaseDep
	loadBalancer *deploy.LoadBalancer

	PluginMap          types.PluginStateMap
	AppStates          map[string]*types.AppState
	DependencyStates   map[string]*types.DependencyState
	destroy, fullCheck bool
}

func NewPlan(pctx *config.PluginContext, logger log.Logger, state types.PluginStateMap, reg *registry.Registry, destroy, fullCheck bool) (*PlanAction, error) {
	if state == nil {
		state = make(types.PluginStateMap)
	}

	for _, t := range gcp.Types {
		err := reg.RegisterType(t)
		if err != nil {
			return nil, err
		}
	}

	return &PlanAction{
		pluginCtx: pctx,
		log:       logger,
		apiRegistry: registry.NewRegistry(&registry.Options{
			Read: fullCheck,
		}),
		registry:       reg,
		appIDMap:       make(map[string]*types.App),
		appDeployIDMap: make(map[string]interface{}),
		appEnvVars:     make(map[string]map[string]interface{}),

		depIDMap:       make(map[string]*types.Dependency),
		depDeployIDMap: make(map[string]interface{}),

		PluginMap:        state,
		AppStates:        make(map[string]*types.AppState),
		DependencyStates: make(map[string]*types.DependencyState),

		destroy:   destroy,
		fullCheck: fullCheck,
	}, nil
}

func (p *PlanAction) planApps(appPlans []*types.AppPlan) error {
	var (
		staticAppsPlan  []*types.AppPlan
		serviceAppsPlan []*types.AppPlan
	)

	for _, app := range appPlans {
		p.appIDMap[app.App.ID] = &app.App.App

		appEnvVars := map[string]interface{}{
			"url": fields.String(app.App.URL),
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

			allNeeds[n.Dependency][&d.App.App] = n
		}
	}

	var (
		databasePlan []*types.DependencyPlan
	)

	for _, dep := range depPlans {
		p.depIDMap[dep.Dependency.ID] = &dep.Dependency.Dependency

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

		err := p.apiRegistry.RegisterPluginResource(deploy.APIName, api, s)
		if err != nil {
			return err
		}
	}

	apiReg := p.PluginMap["api_registry"]

	// Skip Read to avoid being rate limited. And it shouldn't really be necessary to recheck it.
	err := p.apiRegistry.Load(ctx, apiReg, p.pluginCtx)
	if err != nil {
		return err
	}

	diff, err := p.apiRegistry.Diff(ctx)
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

	err = p.registry.Load(ctx, reg, p.pluginCtx)
	if err != nil {
		return err
	}

	return nil
}

func (p *PlanAction) getOrCreateAppState(app *types.App) *types.AppState {
	state, ok := p.AppStates[app.ID]
	if !ok {
		state = types.NewAppState(app)
		p.AppStates[app.ID] = state
	}

	return state
}

func (p *PlanAction) getOrCreateDependencyState(dep *types.Dependency) *types.DependencyState {
	state, ok := p.DependencyStates[dep.ID]
	if !ok {
		state = types.NewDependencyState(dep)
		p.DependencyStates[dep.ID] = state
	}

	return state
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

	var curMapping map[string]interface{}
	if len(p.loadBalancer.URLMaps) > 0 {
		curMapping = p.loadBalancer.URLMaps[0].AppMapping.Current()
	}

	// App SSL states.
	for mapURL, appID := range curMapping {
		id := appID.(string)
		app := p.appIDMap[id]

		if app == nil {
			continue
		}

		state := p.getOrCreateAppState(app)
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

		state.DNS = &types.DNSState{
			IP:            p.loadBalancer.Addresses[0].IP.Current(),
			URL:           mapURL,
			Manual:        true,
			SSLStatus:     sslStatus,
			SSLStatusInfo: sslStatusInfo,
		}
	}

	// App states.
	for id, app := range p.appDeployIDMap {
		deployState := computeAppDeploymentState(app)
		if deployState == nil {
			continue
		}

		state := p.getOrCreateAppState(p.appIDMap[id])
		state.Deployment = deployState
	}

	// Dependency states.
	for id, dep := range p.depDeployIDMap {
		dnsState := computeDependencyDNSState(dep)
		if dnsState == nil {
			continue
		}

		state := p.getOrCreateDependencyState(p.depIDMap[id])
		state.DNS = dnsState
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

	diff, err := p.registry.Diff(ctx)
	if err != nil {
		return nil, err
	}

	var actions []*types.PlanAction
	for _, d := range diff {
		actions = append(actions, d.ToPlanAction())
	}

	err = p.save()
	if err != nil {
		return nil, err
	}

	return &types.Plan{
		Actions: actions,
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

	diff, err := p.registry.Diff(ctx)
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
