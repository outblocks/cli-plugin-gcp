package deploy

import (
	"encoding/json"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/deploy/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/types"
)

type LoadBalancer struct {
	Address      *gcp.Address      `json:"address"`
	Certificates []*gcp.ManagedSSL `json:"managed_ssl" mapstructure:"managed_ssl"`

	NetworkEndpointGroups []*gcp.ServerlessNEG    `json:"network_endpoint_groups" mapstructure:"network_endpoint_groups"`
	BackendServices       []*gcp.BackendService   `json:"backend_services" mapstructure:"backend_services"`
	URLMap                *gcp.URLMap             `json:"url_map" mapstructure:"url_map"`
	TargetHTTPSProxies    []*gcp.TargetHTTPSProxy `json:"target_https_proxies" mapstructure:"target_https_proxies"`
	TargetHTTPProxies     []*gcp.TargetHTTPProxy  `json:"target_http_proxies" mapstructure:"target_http_proxies"`
	ForwardingRules       []*gcp.ForwardingRule   `json:"forwarding_rules" mapstructure:"forwarding_rules"`

	Planned *LoadBalancerPlanned `json:"-" mapstructure:"-"`
}

type LoadBalancerCreate struct {
	Name      string
	ProjectID string
	Region    string
}

type LoadBalancerPlanned struct {
	Address      *gcp.AddressCreate
	Certificates []*gcp.ManagedSSLCreate

	NetworkEndpointGroups []*gcp.ServerlessNEGCreate
	BackendServices       []*gcp.BackendServiceCreate
	URLMap                *gcp.URLMapCreate
	TargetHTTPSProxies    []*gcp.TargetHTTPSProxyCreate
	TargetHTTPProxies     []*gcp.TargetHTTPProxyCreate
	ForwardingRules       []*gcp.ForwardingRuleCreate
}

func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{
		Address: &gcp.Address{},
		URLMap:  &gcp.URLMap{},
		Planned: &LoadBalancerPlanned{},
	}
}

func (o *LoadBalancer) Decode(in interface{}) error {
	err := mapstructure.Decode(in, o)
	if err != nil {
		return err
	}

	return nil
}

func (o *LoadBalancer) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		LoadBalancer
		Type string `json:"type"`
	}{
		LoadBalancer: *o,
		Type:         "load_balancer",
	})
}

func (o *LoadBalancer) Plan(pctx *config.PluginContext, plan *types.PluginPlanActions, c *LoadBalancerCreate, static []*StaticApp, staticPlan []*types.AppPlan, verify bool) error {
	if len(static) == 0 {
		c = nil
	}

	if c != nil {
		o.Planned.Address = &gcp.AddressCreate{
			Name:      gcp.ID(pctx.Env().ProjectName(), c.ProjectID, c.Name),
			ProjectID: c.ProjectID,
		}

		for _, app := range staticPlan {
			domain := strings.SplitN(app.App.URL, "/", 2)[0]

			o.Planned.Certificates = append(o.Planned.Certificates, &gcp.ManagedSSLCreate{
				Name:      gcp.ID(pctx.Env().ProjectName(), c.ProjectID, domain),
				Domain:    domain,
				ProjectID: c.ProjectID,
			})
		}

		for _, app := range static {
			// Serverless NEG creation.
			negC := &gcp.ServerlessNEGCreate{
				Name:      app.Planned.ProxyCloudRun.Name,
				Region:    app.Planned.ProxyCloudRun.Region,
				ProjectID: app.Planned.ProxyCloudRun.ProjectID,
				CloudRun:  app.Planned.ProxyCloudRun.Name,
			}

			o.Planned.NetworkEndpointGroups = append(o.Planned.NetworkEndpointGroups, negC)

			// Backend service creation.
			backendC := &gcp.BackendServiceCreate{
				Name:      negC.Name,
				ProjectID: negC.ProjectID,
				NEG:       negC.ID(),
				Options: &gcp.BackendServiceOptions{
					CDN: gcp.BackendServiceOptionsCDN{
						Enabled: true,
					},
				},
			}

			o.Planned.BackendServices = append(o.Planned.BackendServices, backendC)

			// URL Mapping.
			var mapping []*gcp.URLMapping
			for _, app := range staticPlan {
				mapping = append(mapping, gcp.CreateURLMapping(app.App.URL, backendC.ID()))
			}

			o.Planned.URLMap = &gcp.URLMapCreate{
				Name:      o.Planned.Address.Name,
				ProjectID: c.ProjectID,
				Mapping:   gcp.CleanupURLMapping(mapping),
			}
		}
	}

	// Plan Address.
	err := plan.PlanObject(pctx, o, "Address", o.Planned.Address, verify)
	if err != nil {
		return err
	}

	// Plan Certificates.
	err = plan.PlanObjectList(pctx, o, "Certificates", o.Planned.Certificates, verify)
	if err != nil {
		return err
	}

	// Plan NEG.
	err = plan.PlanObjectList(pctx, o, "NetworkEndpointGroups", o.Planned.NetworkEndpointGroups, verify)
	if err != nil {
		return err
	}

	// Plan Backend Services.
	err = plan.PlanObjectList(pctx, o, "BackendServices", o.Planned.BackendServices, verify)
	if err != nil {
		return err
	}

	// Plan URL Map.
	err = plan.PlanObject(pctx, o, "URLMap", o.Planned.URLMap, verify)
	if err != nil {
		return err
	}

	return nil
}
