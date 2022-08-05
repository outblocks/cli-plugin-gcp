package deploy

import (
	"net/url"
	"sort"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/protobuf/types/known/structpb"
)

type LoadBalancer struct {
	Addresses           []*gcp.Address
	ManagedSSLs         []*gcp.ManagedSSL
	ManagedSSLDomainMap map[string]*gcp.ManagedSSL
	SelfManagedSSLs     []*gcp.SelfManagedSSL
	ServerlessNEGs      []*gcp.ServerlessNEG
	BackendServices     []*gcp.BackendService
	URLMaps             []*gcp.URLMap
	TargetHTTPProxies   []*gcp.TargetHTTPProxy
	TargetHTTPSProxies  []*gcp.TargetHTTPSProxy
	ForwardingRules     []*gcp.ForwardingRule

	urlMap, appMap map[string]fields.Field
}

type LoadBalancerArgs struct {
	Name      string
	ProjectID string
	Region    string
}

func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{
		ManagedSSLDomainMap: make(map[string]*gcp.ManagedSSL),
		urlMap:              make(map[string]fields.Field),
		appMap:              make(map[string]fields.Field),
	}
}

func (o *LoadBalancer) createCloudRunServerlessNEG(pctx *config.PluginContext, appID string, cloudrun fields.StringInputField, c *LoadBalancerArgs) *gcp.ServerlessNEG {
	return &gcp.ServerlessNEG{
		Name:      gcp.IDField(pctx.Env(), appID),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		CloudRun:  cloudrun,
	}
}

func (o *LoadBalancer) createCloudFunctionServerlessNEG(pctx *config.PluginContext, appID string, cloudfunction fields.StringInputField, c *LoadBalancerArgs) *gcp.ServerlessNEG {
	return &gcp.ServerlessNEG{
		Name:          gcp.IDField(pctx.Env(), appID),
		ProjectID:     fields.String(c.ProjectID),
		Region:        fields.String(c.Region),
		CloudFunction: cloudfunction,
	}
}
func (o *LoadBalancer) addServerlessNEG(pctx *config.PluginContext, r *registry.Registry, app *apiv1.App, neg *gcp.ServerlessNEG, cdnEnabled bool, c *LoadBalancerArgs) error {
	_, err := r.RegisterPluginResource(LoadBalancerName, app.Id, neg)
	if err != nil {
		return err
	}

	o.ServerlessNEGs = append(o.ServerlessNEGs, neg)

	// Backend Services.
	svc := &gcp.BackendService{
		Name:      gcp.IDField(pctx.Env(), app.Id),
		ProjectID: fields.String(c.ProjectID),
		NEG:       neg.RefField(),
	}

	svc.CDN.Enabled = fields.Bool(cdnEnabled)

	_, err = r.RegisterPluginResource(LoadBalancerName, app.Id, svc)
	if err != nil {
		return err
	}

	o.BackendServices = append(o.BackendServices, svc)

	// URL Mapping.
	host, path := gcp.SplitURL(app.Url)

	o.urlMap[host+path] = fields.Map(map[string]fields.Field{
		gcp.URLPathMatcherServiceIDKey:         svc.RefField(),
		gcp.URLPathMatcherPathPrefixRewriteKey: fields.String(app.PathRedirect),
	})
	o.appMap[app.Url] = fields.String(app.Id)

	return nil
}

func (o *LoadBalancer) addCloudRun(pctx *config.PluginContext, r *registry.Registry, app *apiv1.App, cloudrun fields.StringInputField, cdnEnabled bool, c *LoadBalancerArgs) error {
	neg := o.createCloudRunServerlessNEG(pctx, app.Id, cloudrun, c)

	return o.addServerlessNEG(pctx, r, app, neg, cdnEnabled, c)
}

func (o *LoadBalancer) addCloudFunction(pctx *config.PluginContext, r *registry.Registry, app *apiv1.App, cloudfunction fields.StringInputField, cdnEnabled bool, c *LoadBalancerArgs) error {
	neg := o.createCloudFunctionServerlessNEG(pctx, app.Id, cloudfunction, c)

	return o.addServerlessNEG(pctx, r, app, neg, cdnEnabled, c)
}

func (o *LoadBalancer) processServiceApps(pctx *config.PluginContext, r *registry.Registry, service map[string]*ServiceApp, c *LoadBalancerArgs) error {
	for _, app := range service {
		if app.App.Url == "" {
			continue
		}

		err := o.addCloudRun(pctx, r, app.App, app.CloudRun.Name, app.Props.CDN.Enabled, c)
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *LoadBalancer) processStaticApps(pctx *config.PluginContext, r *registry.Registry, static map[string]*StaticApp, c *LoadBalancerArgs) error {
	for _, app := range static {
		if app.App.Url == "" {
			continue
		}

		err := o.addCloudRun(pctx, r, app.App, app.CloudRun.Name, app.Props.CDN.Enabled, c)
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *LoadBalancer) processFunctionApps(pctx *config.PluginContext, r *registry.Registry, function map[string]*FunctionApp, c *LoadBalancerArgs) error {
	for _, app := range function {
		if app.App.Url == "" {
			continue
		}

		err := o.addCloudFunction(pctx, r, app.App, app.CloudFunction.Name, app.Props.CDN.Enabled, c)
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *LoadBalancer) processDomain(pctx *config.PluginContext, r *registry.Registry, c *LoadBalancerArgs, domain string, domainInfo *apiv1.DomainInfo) error {
	if domainInfo == nil || domainInfo.Cert == "" || domainInfo.Key == "" {
		cert := &gcp.ManagedSSL{
			Name:      gcp.IDField(pctx.Env(), domain),
			ProjectID: fields.String(c.ProjectID),
			Domains:   fields.Array([]fields.Field{fields.String(domain)}),
		}

		_, err := r.RegisterPluginResource(LoadBalancerName, domain, cert)
		if err != nil {
			return err
		}

		if domainInfo != nil {
			if domainInfo.Properties.GetFields() == nil {
				domainInfo.Properties, _ = structpb.NewStruct(nil)
			}

			domainInfo.Properties.GetFields()["cloudflare_proxy"] = structpb.NewBoolValue(false)
		}

		o.ManagedSSLs = append(o.ManagedSSLs, cert)
		o.ManagedSSLDomainMap[domain] = cert

		return nil
	}

	// Create new self managed cert if needed.
	name := "self-managed-" + plugin_util.LimitString(plugin_util.SHAString(domainInfo.Cert), 8)
	selfManagedCert := &gcp.SelfManagedSSL{
		Name:        gcp.IDField(pctx.Env(), name),
		ProjectID:   fields.String(c.ProjectID),
		Certificate: fields.String(domainInfo.Cert),
		PrivateKey:  fields.String(domainInfo.Key),
	}

	added, err := r.RegisterPluginResource(LoadBalancerName, name, selfManagedCert)
	if err != nil {
		return err
	}

	if added {
		o.SelfManagedSSLs = append(o.SelfManagedSSLs, selfManagedCert)
	}

	return nil
}

func (o *LoadBalancer) planHTTP(pctx *config.PluginContext, r *registry.Registry, addr *gcp.Address, c *LoadBalancerArgs) error {
	// URL Map.
	mhttp := &gcp.URLMap{
		Name:          gcp.IDField(pctx.Env(), c.Name+"-http-0"),
		ProjectID:     fields.String(c.ProjectID),
		HTTPSRedirect: fields.Bool(true),
	}

	_, err := r.RegisterPluginResource(LoadBalancerName, c.Name+"-http-0", mhttp)
	if err != nil {
		return err
	}

	// Target HTTP Proxy.
	proxy := &gcp.TargetHTTPProxy{
		Name:      gcp.IDField(pctx.Env(), c.Name+"-0"),
		ProjectID: fields.String(c.ProjectID),
		URLMap:    mhttp.RefField(),
	}

	_, err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-0", proxy)
	if err != nil {
		return err
	}

	o.TargetHTTPProxies = append(o.TargetHTTPProxies, proxy)

	// HTTP forwarding Rules.
	rule := &gcp.ForwardingRule{
		Name:      gcp.IDField(pctx.Env(), c.Name+"-http-0"),
		ProjectID: fields.String(c.ProjectID),
		IPAddress: addr.IP.Input(),
		Target:    proxy.RefField(),
		PortRange: fields.String("80-80"),
	}

	_, err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-http-0", rule)
	if err != nil {
		return err
	}

	o.ForwardingRules = append(o.ForwardingRules, rule)

	return nil
}

func (o *LoadBalancer) Plan(pctx *config.PluginContext, r *registry.Registry, static map[string]*StaticApp, service map[string]*ServiceApp, function map[string]*FunctionApp, domainMatch *types.DomainInfoMatcher, c *LoadBalancerArgs) error {
	staticApps := make([]*StaticApp, 0, len(static))
	serviceApps := make([]*ServiceApp, 0, len(service))
	functionApps := make([]*FunctionApp, 0, len(function))

	// Process Apps in LB.
	err := o.processStaticApps(pctx, r, static, c)
	if err != nil {
		return err
	}

	err = o.processServiceApps(pctx, r, service, c)
	if err != nil {
		return err
	}

	err = o.processFunctionApps(pctx, r, function, c)
	if err != nil {
		return err
	}

	if len(o.urlMap) == 0 && len(o.appMap) == 0 {
		return nil
	}

	// IP Address.
	addr := &gcp.Address{
		Name:      gcp.IDField(pctx.Env(), c.Name+"-0"),
		ProjectID: fields.String(c.ProjectID),
	}

	_, err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-0", addr)
	if err != nil {
		return err
	}

	o.Addresses = append(o.Addresses, addr)

	// Certificates.
	domainsList := make(map[string]struct{})

	for _, app := range static {
		u, _ := url.Parse(app.App.Url)
		domainsList[u.Hostname()] = struct{}{}

		staticApps = append(staticApps, app)
	}

	for _, app := range service {
		if app.App.Url == "" {
			continue
		}

		u, _ := url.Parse(app.App.Url)
		domainsList[u.Hostname()] = struct{}{}

		serviceApps = append(serviceApps, app)
	}

	// Sort domains to make sure state remains the same.
	var domainList []string
	for domain := range domainsList {
		domainList = append(domainList, domain)
	}

	sort.Strings(domainList)

	for _, domain := range domainList {
		err = o.processDomain(pctx, r, c, domain, domainMatch.Match(domain))
		if err != nil {
			return err
		}
	}

	err = o.planHTTP(pctx, r, addr, c)
	if err != nil {
		return err
	}

	// URL Map.
	mhttps := &gcp.URLMap{
		Name:       gcp.IDField(pctx.Env(), c.Name+"-https-0"),
		ProjectID:  fields.String(c.ProjectID),
		URLMapping: fields.Map(o.urlMap),
		AppMapping: fields.Map(o.appMap),
	}

	_, err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-https-0", mhttps)
	if err != nil {
		return err
	}

	_, err = r.RegisterPluginResource(LoadBalancerName, c.Name, &CacheInvalidate{
		URLMapName:   mhttps.Name,
		ProjectID:    fields.String(c.ProjectID),
		StaticApps:   staticApps,
		ServiceApps:  serviceApps,
		FunctionApps: functionApps,
	})
	if err != nil {
		return err
	}

	o.URLMaps = append(o.URLMaps, mhttps)

	// Target HTTPS Proxy.
	var certs []fields.Field
	for _, cert := range o.ManagedSSLs {
		certs = append(certs, cert.RefField())
	}

	for _, cert := range o.SelfManagedSSLs {
		certs = append(certs, cert.RefField())
	}

	sproxy := &gcp.TargetHTTPSProxy{
		Name:            gcp.IDField(pctx.Env(), c.Name+"-0"),
		ProjectID:       fields.String(c.ProjectID),
		URLMap:          mhttps.RefField(),
		SSLCertificates: fields.Array(certs),
	}

	_, err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-0", sproxy)
	if err != nil {
		return err
	}

	o.TargetHTTPSProxies = append(o.TargetHTTPSProxies, sproxy)

	// HTTPS forwarding rule.
	rule := &gcp.ForwardingRule{
		Name:      gcp.IDField(pctx.Env(), c.Name+"-https-0"),
		ProjectID: fields.String(c.ProjectID),
		IPAddress: addr.IP.Input(),
		Target:    sproxy.RefField(),
		PortRange: fields.String("443-443"),
	}

	_, err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-https-0", rule)
	if err != nil {
		return err
	}

	o.ForwardingRules = append(o.ForwardingRules, rule)

	return nil
}
