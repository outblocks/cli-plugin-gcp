package deploy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/deploy/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/types"
)

type LoadBalancer struct {
	Addresses    []*gcp.Address    `json:"addresses"`
	Certificates []*gcp.ManagedSSL `json:"managed_ssl" mapstructure:"managed_ssl"`

	NetworkEndpointGroups []*gcp.ServerlessNEG    `json:"network_endpoint_groups" mapstructure:"network_endpoint_groups"`
	BackendServices       []*gcp.BackendService   `json:"backend_services" mapstructure:"backend_services"`
	URLMap                *gcp.URLMap             `json:"url_map" mapstructure:"url_map"`
	TargetHTTPSProxies    []*gcp.TargetHTTPSProxy `json:"target_https_proxies" mapstructure:"target_https_proxies"`
	TargetHTTPProxy       *gcp.TargetHTTPProxy    `json:"target_http_proxy" mapstructure:"target_http_proxy"`

	Planned *LoadBalancerPlanned `json:"-" mapstructure:"-"`
}

type LoadBalancerCreate struct {
	Name      string
	ProjectID string
	Region    string
}

type LoadBalancerPlanned struct {
	Addresses    []*gcp.AddressCreate
	Certificates []*gcp.ManagedSSLCreate

	NetworkEndpointGroups []*gcp.ServerlessNEGCreate
	BackendServices       []*gcp.BackendServiceCreate
	URLMap                *gcp.URLMapCreate
	TargetHTTPSProxies    []*gcp.TargetHTTPSProxyCreate
	TargetHTTPProxy       *gcp.TargetHTTPProxyCreate
}

func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{
		URLMap:          &gcp.URLMap{},
		TargetHTTPProxy: &gcp.TargetHTTPProxy{},

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
		lbID := gcp.ID(pctx.Env().ProjectName(), c.ProjectID, c.Name)

		// Certificates.
		domains := make(map[string]struct{})

		for _, app := range staticPlan {
			domain := strings.SplitN(app.App.URL, "/", 2)[0]
			domains[domain] = struct{}{}
		}

		for domain := range domains {
			o.Planned.Certificates = append(o.Planned.Certificates, &gcp.ManagedSSLCreate{
				Name:      gcp.ID(pctx.Env().ProjectName(), c.ProjectID, domain),
				Domain:    domain,
				ProjectID: c.ProjectID,
			})
		}

		var mapping []*gcp.URLMapping

		for i, app := range static {
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
						Enabled: false,
					},
				},
			}

			o.Planned.BackendServices = append(o.Planned.BackendServices, backendC)

			// URL Mapping.
			mapping = append(mapping, gcp.CreateURLMapping(staticPlan[i].App.URL, backendC.ID()))
		}

		o.Planned.URLMap = &gcp.URLMapCreate{
			Name:      lbID,
			ProjectID: c.ProjectID,
			Mapping:   gcp.CleanupURLMapping(mapping),
		}

		// TargetHTTPProxy.
		o.Planned.TargetHTTPProxy = &gcp.TargetHTTPProxyCreate{
			Name:      lbID,
			ProjectID: c.ProjectID,
			URLMap:    o.Planned.URLMap.ID(),
		}

		// TargetHTTPSProxies.
		var certIDs []string
		for _, cert := range o.Planned.Certificates {
			certIDs = append(certIDs, cert.ID())
		}

		sort.Strings(certIDs)

		for i := 0; len(certIDs) > i; i += 15 {
			r := i + 15
			if r > len(certIDs) {
				r = len(certIDs)
			}

			o.Planned.TargetHTTPSProxies = append(o.Planned.TargetHTTPSProxies, &gcp.TargetHTTPSProxyCreate{
				Name:            gcp.ID(pctx.Env().ProjectName(), c.ProjectID, c.Name) + fmt.Sprintf("-%d", i/15),
				ProjectID:       c.ProjectID,
				URLMap:          o.Planned.URLMap.ID(),
				SSLCertificates: certIDs[i:r],
			})
		}

		// Addresses.
		var rules []*gcp.AddressForwardingRule

		rules = append(rules, &gcp.AddressForwardingRule{
			Name:      o.Planned.TargetHTTPProxy.Name,
			Target:    o.Planned.TargetHTTPProxy.ID(),
			PortRange: "80-80",
		})

		o.Planned.Addresses = append(o.Planned.Addresses, &gcp.AddressCreate{
			Name:            lbID + "-0",
			ProjectID:       c.ProjectID,
			ForwardingRules: rules,
		})
	}

	// Plan Certificates.
	err := plan.PlanObjectList(pctx, o, "Certificates", o.Planned.Certificates, verify)
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

	// Plan TargetHTTPProxy.
	err = plan.PlanObject(pctx, o, "TargetHTTPProxy", o.Planned.TargetHTTPProxy, verify)
	if err != nil {
		return err
	}

	// Plan TargetHTTPProxies.
	err = plan.PlanObjectList(pctx, o, "TargetHTTPSProxies", o.Planned.TargetHTTPSProxies, verify)
	if err != nil {
		return err
	}

	// Plan Address.
	err = plan.PlanObjectList(pctx, o, "Addresses", o.Planned.Addresses, verify)
	if err != nil {
		return err
	}

	return nil
}
