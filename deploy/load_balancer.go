package deploy

import (
	"sort"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
)

type LoadBalancer struct {
	Addresses          []*gcp.Address
	ManagedSSLs        []*gcp.ManagedSSL
	ServerlessNEGs     []*gcp.ServerlessNEG
	BackendServices    []*gcp.BackendService
	URLMaps            []*gcp.URLMap
	TargetHTTPProxies  []*gcp.TargetHTTPProxy
	TargetHTTPSProxies []*gcp.TargetHTTPSProxy
	ForwardingRules    []*gcp.ForwardingRule
}

type LoadBalancerArgs struct {
	Name      string
	ProjectID string
	Region    string
}

func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{}
}

func (o *LoadBalancer) Plan(pctx *config.PluginContext, r *registry.Registry, static map[string]*StaticApp, c *LoadBalancerArgs, verify bool) error {
	if len(static) == 0 {
		return nil
	}

	lbID := gcp.ID(pctx.Env().ProjectName(), c.ProjectID, c.Name)
	staticApps := make([]*StaticApp, 0, len(static))

	// IP Address.
	addr := &gcp.Address{
		Name:      fields.String(lbID + "-0"),
		ProjectID: fields.String(c.ProjectID),
	}

	err := r.Register(addr, LoadBalancerName, lbID+"-0")
	if err != nil {
		return err
	}

	o.Addresses = append(o.Addresses, addr)

	// Certificates.
	domains := make(map[string]struct{})

	for _, app := range static {
		domain := strings.SplitN(app.App.URL, "/", 2)[0]
		domains[domain] = struct{}{}

		staticApps = append(staticApps, app)
	}

	// Sort domains to make sure state remains the same.
	var domainList []string
	for domain := range domains {
		domainList = append(domainList, domain)
	}

	sort.Strings(domainList)

	for _, domain := range domainList {
		cert := &gcp.ManagedSSL{
			Name:      fields.String(gcp.ID(pctx.Env().ProjectName(), c.ProjectID, domain)),
			ProjectID: fields.String(c.ProjectID),
			Domain:    fields.String(domain),
		}

		err := r.Register(cert, LoadBalancerName, domain)
		if err != nil {
			return err
		}

		o.ManagedSSLs = append(o.ManagedSSLs, cert)
	}

	urlMap := make(map[string]fields.Field)

	for _, app := range static {
		// Serverless NEGs.
		neg := &gcp.ServerlessNEG{
			Name:      fields.String(gcp.ID(pctx.Env().ProjectName(), c.ProjectID, app.App.ID)),
			ProjectID: fields.String(c.ProjectID),
			Region:    fields.String(c.Region),
			CloudRun:  app.CloudRun.Name,
		}

		err := r.Register(neg, LoadBalancerName, app.App.ID)
		if err != nil {
			return err
		}

		o.ServerlessNEGs = append(o.ServerlessNEGs, neg)

		// Backend Services.
		svc := &gcp.BackendService{
			Name:      fields.String(gcp.ID(pctx.Env().ProjectName(), c.ProjectID, app.App.ID)),
			ProjectID: fields.String(c.ProjectID),
			NEG:       neg.ID(),
		}

		svc.CDN.Enabled = fields.Bool(app.Opts.CDN.Enabled)

		err = r.Register(svc, LoadBalancerName, app.App.ID)
		if err != nil {
			return err
		}

		o.BackendServices = append(o.BackendServices, svc)

		// URL Mapping.
		url := app.App.URL
		if strings.Count(url, "/") == 1 {
			url += "*"
		}

		urlMap[url] = svc.ID()
	}

	// URL Map.
	m := &gcp.URLMap{
		Name:       fields.String(lbID + "-0"),
		ProjectID:  fields.String(c.ProjectID),
		URLMapping: fields.Map(urlMap),
	}

	err = r.Register(m, LoadBalancerName, lbID+"-0")
	if err != nil {
		return err
	}

	err = r.Register(&CacheInvalidate{
		URLMapName: m.Name,
		ProjectID:  fields.String(c.ProjectID),
		StaticApps: staticApps,
	}, LoadBalancerName, lbID)
	if err != nil {
		return err
	}

	o.URLMaps = append(o.URLMaps, m)

	// Target HTTP Proxy.
	proxy := &gcp.TargetHTTPProxy{
		Name:      fields.String(lbID + "-0"),
		ProjectID: fields.String(c.ProjectID),
		URLMap:    m.ID(),
	}

	err = r.Register(proxy, LoadBalancerName, lbID+"-0")
	if err != nil {
		return err
	}

	o.TargetHTTPProxies = append(o.TargetHTTPProxies, proxy)

	// Target HTTPS Proxy.
	var certs []fields.Field
	for _, cert := range o.ManagedSSLs {
		certs = append(certs, cert.ID())
	}

	sproxy := &gcp.TargetHTTPSProxy{
		Name:            fields.String(lbID + "-0"),
		ProjectID:       fields.String(c.ProjectID),
		URLMap:          m.ID(),
		SSLCertificates: fields.Array(certs),
	}

	err = r.Register(sproxy, LoadBalancerName, lbID+"-0")
	if err != nil {
		return err
	}

	o.TargetHTTPSProxies = append(o.TargetHTTPSProxies, sproxy)

	// HTTP forwarding Rules.
	rule := &gcp.ForwardingRule{
		Name:      fields.String(lbID + "-http-0"),
		ProjectID: fields.String(c.ProjectID),
		IPAddress: addr.IP,
		Target:    proxy.ID(),
		PortRange: fields.String("80-80"),
	}

	err = r.Register(rule, LoadBalancerName, lbID+"-http-0")
	if err != nil {
		return err
	}

	o.ForwardingRules = append(o.ForwardingRules, rule)

	// HTTPS forwarding rule.
	rule = &gcp.ForwardingRule{
		Name:      fields.String(lbID + "-https-0"),
		ProjectID: fields.String(c.ProjectID),
		IPAddress: addr.IP,
		Target:    sproxy.ID(),
		PortRange: fields.String("443-443"),
	}

	err = r.Register(rule, LoadBalancerName, lbID+"-https-0")
	if err != nil {
		return err
	}

	o.ForwardingRules = append(o.ForwardingRules, rule)

	return nil
}
