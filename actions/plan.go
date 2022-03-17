package actions

import (
	"context"
	"fmt"
	"net/url"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
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
	appIDMap       map[string]*apiv1.App
	appDeployIDMap map[string]interface{}
	appEnvVars     types.AppVars

	depIDMap       map[string]*apiv1.Dependency
	depDeployIDMap map[string]interface{}

	staticApps   map[string]*deploy.StaticApp
	serviceApps  map[string]*deploy.ServiceApp
	databaseDeps map[string]*deploy.DatabaseDep
	storageDeps  map[string]*deploy.StorageDep
	loadBalancer *deploy.LoadBalancer

	cloudRunSettings *deploy.CloudRunSettings
	dnsRecordsMap    map[string]*apiv1.DNSRecord

	State              *apiv1.PluginState
	domainMatcher      *types.DomainInfoMatcher
	AppStates          map[string]*apiv1.AppState
	DependencyStates   map[string]*apiv1.DependencyState
	DNSRecords         []*apiv1.DNSRecord
	destroy, fullCheck bool
}

func NewPlan(pctx *config.PluginContext, logger log.Logger, state *apiv1.PluginState, domains []*apiv1.DomainInfo, reg *registry.Registry, destroy, fullCheck bool) (*PlanAction, error) {
	if state == nil {
		state = types.NewPluginState()
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
		appIDMap:       make(map[string]*apiv1.App),
		appDeployIDMap: make(map[string]interface{}),

		depIDMap:       make(map[string]*apiv1.Dependency),
		depDeployIDMap: make(map[string]interface{}),
		dnsRecordsMap:  make(map[string]*apiv1.DNSRecord),

		State:            state,
		domainMatcher:    types.NewDomainInfoMatcher(domains),
		AppStates:        make(map[string]*apiv1.AppState),
		DependencyStates: make(map[string]*apiv1.DependencyState),

		destroy:   destroy,
		fullCheck: fullCheck,
	}, nil
}

func (p *PlanAction) planApps(ctx context.Context, appPlans []*apiv1.AppPlan, apply bool) error {
	var (
		staticAppsPlan  []*apiv1.AppPlan
		serviceAppsPlan []*apiv1.AppPlan
	)

	apps := make([]*apiv1.App, 0, len(appPlans))

	for _, plan := range appPlans {
		p.appIDMap[plan.State.App.Id] = plan.State.App
		apps = append(apps, plan.State.App)

		switch plan.State.App.Type {
		case TypeStatic:
			staticAppsPlan = append(staticAppsPlan, plan)
		case TypeService:
			serviceAppsPlan = append(serviceAppsPlan, plan)
		}
	}

	p.appEnvVars = types.AppVarsFromApps(apps)

	var err error

	// Plan static app deployment.
	p.staticApps, err = p.planStaticAppsDeploy(staticAppsPlan)
	if err != nil {
		return err
	}

	// Plan service app deployment.
	p.serviceApps, err = p.planServiceAppsDeploy(ctx, serviceAppsPlan, apply)
	if err != nil {
		return err
	}

	return nil
}

func (p *PlanAction) planDependencies(appPlans []*apiv1.AppPlan, depPlans []*apiv1.DependencyPlan) error {
	allNeeds := make(map[string]map[*apiv1.App]*apiv1.AppNeed)

	for _, plan := range appPlans {
		for _, n := range plan.State.App.Needs {
			if _, ok := allNeeds[n.Dependency]; !ok {
				allNeeds[n.Dependency] = make(map[*apiv1.App]*apiv1.AppNeed)
			}

			allNeeds[n.Dependency][plan.State.App] = n
		}
	}

	var databasePlan, storagePlan []*apiv1.DependencyPlan

	for _, plan := range depPlans {
		p.depIDMap[plan.State.Dependency.Id] = plan.State.Dependency

		switch plan.State.Dependency.Type {
		case DepTypePostgresql, DepTypeMySQL:
			databasePlan = append(databasePlan, plan)
		case DepTypeStorage:
			storagePlan = append(storagePlan, plan)
		}
	}

	var err error

	// Plan dependency deployment.
	p.databaseDeps, err = p.planDatabaseDepsDeploy(databasePlan, allNeeds)
	if err != nil {
		return err
	}

	p.storageDeps, err = p.planStorageDepsDeploy(storagePlan, allNeeds)
	if err != nil {
		return err
	}

	return nil
}

func (p *PlanAction) enableAPIs(ctx context.Context) error {
	// Process API registry.
	for _, api := range gcp.APISRequired {
		s := &gcp.APIService{Name: fields.String(api)}

		_, err := p.apiRegistry.RegisterPluginResource(deploy.APIName, api, s)
		if err != nil {
			return err
		}
	}

	if p.State.Other == nil {
		p.State.Other = make(map[string][]byte)
	}

	apiReg := p.State.Other["api_registry"]

	// Skip Read to avoid being rate limited. And it shouldn't really be necessary to recheck it.
	err := p.apiRegistry.Load(ctx, apiReg)
	if err != nil {
		return err
	}

	err = p.apiRegistry.Process(ctx, p.pluginCtx)
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

	p.State.Other["api_registry"] = data

	return nil
}

func (p *PlanAction) planAll(ctx context.Context, appPlans []*apiv1.AppPlan, depPlans []*apiv1.DependencyPlan, apply bool) error {
	reg := p.State.Registry

	err := p.registry.Load(ctx, reg)
	if err != nil {
		return err
	}

	// Plan all.
	err = p.planDependencies(appPlans, depPlans)
	if err != nil {
		return err
	}

	err = p.planApps(ctx, appPlans, apply)
	if err != nil {
		return err
	}

	p.loadBalancer = deploy.NewLoadBalancer()

	err = p.loadBalancer.Plan(p.pluginCtx, p.registry, p.staticApps, p.serviceApps, p.domainMatcher, &deploy.LoadBalancerArgs{
		Name:      "load_balancer",
		ProjectID: p.pluginCtx.Settings().ProjectID,
		Region:    p.pluginCtx.Settings().Region,
	})
	if err != nil {
		return err
	}

	// Process registry.
	err = p.registry.Process(ctx, p.pluginCtx)
	if err != nil {
		return err
	}

	return nil
}

func (p *PlanAction) getOrCreateAppState(app *apiv1.App) *apiv1.AppState {
	state, ok := p.AppStates[app.Id]
	if !ok {
		state = &apiv1.AppState{
			App: app,
		}
		p.AppStates[app.Id] = state
	}

	return state
}

func (p *PlanAction) getOrCreateDependencyState(dep *apiv1.Dependency) *apiv1.DependencyState {
	state, ok := p.DependencyStates[dep.Id]
	if !ok {
		state = &apiv1.DependencyState{
			Dependency: dep,
		}
		p.DependencyStates[dep.Id] = state
	}

	return state
}

func (p *PlanAction) saveAppSSLStates(curMapping map[string]interface{}) {
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
		sslStatus := apiv1.DNSState_SSL_STATUS_UNSPECIFIED
		sslStatusInfo := ""

		if ssl != nil {
			switch ssl.Status.Current() {
			case "ACTIVE":
				sslStatus = apiv1.DNSState_SSL_STATUS_OK
			case "PROVISIONING":
				sslStatus = apiv1.DNSState_SSL_STATUS_PROVISIONING
			case "PROVISIONING_FAILED", "PROVISIONING_FAILED_PERMANENTLY":
				sslStatus = apiv1.DNSState_SSL_STATUS_PROVISIONING_FAILED
			case "RENEWAL_FAILED":
				sslStatus = apiv1.DNSState_SSL_STATUS_RENEWAL_FAILED
			}

			if v, ok := ssl.DomainStatus.Current()[domain]; ok {
				sslStatusInfo = v.(string)
			}
		}

		state.Dns = &apiv1.DNSState{
			Ip:            p.loadBalancer.Addresses[0].IP.Current(),
			Url:           mapURL,
			SslStatus:     sslStatus,
			SslStatusInfo: sslStatusInfo,
		}

		p.dnsRecordsMap[domain] = &apiv1.DNSRecord{
			Record: domain,
			Type:   apiv1.DNSRecord_TYPE_A,
			Value:  p.loadBalancer.Addresses[0].IP.Current(),
		}
	}

	for _, v := range p.dnsRecordsMap {
		p.DNSRecords = append(p.DNSRecords, v)
	}
}

func (p *PlanAction) save() error {
	data, err := p.registry.Dump()
	if err != nil {
		return err
	}

	p.State.Registry = data

	if p.destroy {
		return nil
	}

	var curMapping map[string]interface{}
	if len(p.loadBalancer.URLMaps) > 0 {
		curMapping = p.loadBalancer.URLMaps[0].AppMapping.Current()
	}

	// App SSL states.
	p.saveAppSSLStates(curMapping)

	// App states.
	for id, app := range p.appDeployIDMap {
		deployState := computeAppDeploymentState(app)
		if deployState == nil {
			continue
		}

		state := p.getOrCreateAppState(p.appIDMap[id])
		state.Deployment = deployState

		if state.Dns == nil {
			state.Dns = &apiv1.DNSState{}
		}

		switch a := app.(type) {
		case *deploy.ServiceApp:
			if a.Props.Private {
				state.Dns.InternalUrl = fmt.Sprintf("http://%s/", a.CloudRun.Name.Current())
			} else {
				state.Dns.CloudUrl = a.CloudRun.URL.Current()
			}

		case *deploy.StaticApp:
			state.Dns.CloudUrl = a.CloudRun.URL.Current()
		}
	}

	// Dependency states.
	for id, dep := range p.depDeployIDMap {
		state := p.getOrCreateDependencyState(p.depIDMap[id])

		dnsState := computeDependencyDNSState(dep)
		if dnsState == nil {
			continue
		}

		state.Dns = dnsState
	}

	return nil
}

func (p *PlanAction) Plan(ctx context.Context, appPlans []*apiv1.AppPlan, depPlans []*apiv1.DependencyPlan) (*apiv1.Plan, error) {
	err := p.prepareCloudRunURL(ctx, false)
	if err != nil {
		return nil, err
	}

	err = p.enableAPIs(ctx)
	if err != nil {
		return nil, err
	}

	err = p.planAll(ctx, appPlans, depPlans, false)
	if err != nil {
		return nil, err
	}

	diff, err := p.registry.Diff(ctx)
	if err != nil {
		return nil, err
	}

	var actions []*apiv1.PlanAction
	for _, d := range diff {
		actions = append(actions, d.ToPlanAction())
	}

	err = p.save()
	if err != nil {
		return nil, err
	}

	return &apiv1.Plan{
		Actions: actions,
	}, nil
}

func (p *PlanAction) Apply(ctx context.Context, appPlans []*apiv1.AppPlan, depPlans []*apiv1.DependencyPlan, cb func(a *apiv1.ApplyAction)) error {
	err := p.prepareCloudRunURL(ctx, !p.destroy)
	if err != nil {
		return err
	}

	err = p.enableAPIs(ctx)
	if err != nil {
		return err
	}

	err = p.planAll(ctx, appPlans, depPlans, true)
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
