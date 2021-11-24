package deploy

import (
	"net/url"
	"sort"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
)

type LoadBalancer struct {
	Addresses           []*gcp.Address
	ManagedSSLs         []*gcp.ManagedSSL
	ManagedSSLDomainMap map[string]*gcp.ManagedSSL
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

func (o *LoadBalancer) addCloudRun(pctx *config.PluginContext, r *registry.Registry, app *apiv1.App, cloudrun fields.StringInputField, cdnEnabled bool, c *LoadBalancerArgs) error {
	// Serverless NEGs.
	neg := &gcp.ServerlessNEG{
		Name:      gcp.IDField(pctx.Env(), app.Id),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		CloudRun:  cloudrun,
	}

	err := r.RegisterPluginResource(LoadBalancerName, app.Id, neg)
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

	err = r.RegisterPluginResource(LoadBalancerName, app.Id, svc)
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

func (o *LoadBalancer) processServiceApps(pctx *config.PluginContext, r *registry.Registry, service map[string]*ServiceApp, c *LoadBalancerArgs) error {
	for _, app := range service {
		if !app.CloudRun.IsPublic.Wanted() {
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
		err := o.addCloudRun(pctx, r, app.App, app.CloudRun.Name, app.Props.CDN.Enabled, c)
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *LoadBalancer) Plan(pctx *config.PluginContext, r *registry.Registry, static map[string]*StaticApp, service map[string]*ServiceApp, c *LoadBalancerArgs) error {
	if len(static) == 0 && len(service) == 0 {
		return nil
	}

	staticApps := make([]*StaticApp, 0, len(static))
	serviceApps := make([]*ServiceApp, 0, len(service))

	// IP Address.
	addr := &gcp.Address{
		Name:      gcp.IDField(pctx.Env(), c.Name+"-0"),
		ProjectID: fields.String(c.ProjectID),
	}

	err := r.RegisterPluginResource(LoadBalancerName, c.Name+"-0", addr)
	if err != nil {
		return err
	}

	o.Addresses = append(o.Addresses, addr)

	// Certificates.
	domains := make(map[string]struct{})

	for _, app := range static {
		u, _ := url.Parse(app.App.Url)
		domains[u.Hostname()] = struct{}{}

		staticApps = append(staticApps, app)
	}

	for _, app := range service {
		u, _ := url.Parse(app.App.Url)
		domains[u.Hostname()] = struct{}{}

		serviceApps = append(serviceApps, app)
	}

	// Sort domains to make sure state remains the same.
	var domainList []string
	for domain := range domains {
		domainList = append(domainList, domain)
	}

	sort.Strings(domainList)

	for _, domain := range domainList {
		cert := &gcp.ManagedSSL{
			Name:      gcp.IDField(pctx.Env(), domain),
			ProjectID: fields.String(c.ProjectID),
			Domains:   fields.Array([]fields.Field{fields.String(domain)}),
		}

		err := r.RegisterPluginResource(LoadBalancerName, domain, cert)
		if err != nil {
			return err
		}

		o.ManagedSSLs = append(o.ManagedSSLs, cert)
		o.ManagedSSLDomainMap[domain] = cert
	}

	// Process Apps in LB.
	err = o.processStaticApps(pctx, r, static, c)
	if err != nil {
		return err
	}

	err = o.processServiceApps(pctx, r, service, c)
	if err != nil {
		return err
	}

	// URL Map.
	m := &gcp.URLMap{
		Name:       gcp.IDField(pctx.Env(), c.Name+"-0"),
		ProjectID:  fields.String(c.ProjectID),
		URLMapping: fields.Map(o.urlMap),
		AppMapping: fields.Map(o.appMap),
	}

	err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-0", m)
	if err != nil {
		return err
	}

	err = r.RegisterPluginResource(LoadBalancerName, c.Name, &CacheInvalidate{
		URLMapName:  m.Name,
		ProjectID:   fields.String(c.ProjectID),
		StaticApps:  staticApps,
		ServiceApps: serviceApps,
	})
	if err != nil {
		return err
	}

	o.URLMaps = append(o.URLMaps, m)

	// Target HTTP Proxy.
	proxy := &gcp.TargetHTTPProxy{
		Name:      gcp.IDField(pctx.Env(), c.Name+"-0"),
		ProjectID: fields.String(c.ProjectID),
		URLMap:    m.RefField(),
	}

	err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-0", proxy)
	if err != nil {
		return err
	}

	o.TargetHTTPProxies = append(o.TargetHTTPProxies, proxy)

	// Target HTTPS Proxy.
	var certs []fields.Field
	for _, cert := range o.ManagedSSLs {
		certs = append(certs, cert.RefField())
	}

	sproxy := &gcp.TargetHTTPSProxy{
		Name:            gcp.IDField(pctx.Env(), c.Name+"-0"),
		ProjectID:       fields.String(c.ProjectID),
		URLMap:          m.RefField(),
		SSLCertificates: fields.Array(certs),
	}

	err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-0", sproxy)
	if err != nil {
		return err
	}

	o.TargetHTTPSProxies = append(o.TargetHTTPSProxies, sproxy)

	// HTTP forwarding Rules.
	rule := &gcp.ForwardingRule{
		Name:      gcp.IDField(pctx.Env(), c.Name+"-http-0"),
		ProjectID: fields.String(c.ProjectID),
		IPAddress: addr.IP.Input(),
		Target:    proxy.RefField(),
		PortRange: fields.String("80-80"),
	}

	err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-http-0", rule)
	if err != nil {
		return err
	}

	o.ForwardingRules = append(o.ForwardingRules, rule)

	// HTTPS forwarding rule.
	rule = &gcp.ForwardingRule{
		Name:      gcp.IDField(pctx.Env(), c.Name+"-https-0"),
		ProjectID: fields.String(c.ProjectID),
		IPAddress: addr.IP.Input(),
		Target:    sproxy.RefField(),
		PortRange: fields.String("443-443"),
	}

	err = r.RegisterPluginResource(LoadBalancerName, c.Name+"-https-0", rule)
	if err != nil {
		return err
	}

	o.ForwardingRules = append(o.ForwardingRules, rule)

	return nil
}
