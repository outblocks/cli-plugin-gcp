package deploy

import (
	"encoding/json"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/deploy/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/types"
)

type LoadBalancerCreate struct {
	Name      string
	ProjectID string
	Region    string
}

type LoadBalancer struct {
	Address      *gcp.Address      `json:"address"`
	Certificates []*gcp.ManagedSSL `json:"managed_ssl" mapstructure:"managed_ssl"`

	NetworkEndpointGroups []*gcp.ServerlessNEG    `json:"network_endpoint_groups" mapstructure:"network_endpoint_groups"`
	BackendServices       []*gcp.BackendService   `json:"backend_services" mapstructure:"backend_services"`
	URLMaps               []*gcp.URLMap           `json:"url_maps" mapstructure:"url_maps"`
	TargetHTTPSProxies    []*gcp.TargetHTTPSProxy `json:"target_https_proxies" mapstructure:"target_https_proxies"`
	TargetHTTPProxies     []*gcp.TargetHTTPProxy  `json:"target_http_proxies" mapstructure:"target_http_proxies"`
	ForwardingRules       []*gcp.ForwardingRules  `json:"forwarding_rules" mapstructure:"forwarding_rules"`
}

func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{
		Address: &gcp.Address{},
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
	var (
		addressCreate         *gcp.AddressCreate
		certsCreate           []*gcp.ManagedSSLCreate
		negCreate             []*gcp.ServerlessNEGCreate
		backendServicesCreate []*gcp.BackendServiceCreate
	)

	if len(static) == 0 {
		c = nil
	}

	if c != nil {
		addressCreate = &gcp.AddressCreate{
			Name:      gcp.ID(pctx.Env().ProjectName(), c.ProjectID, c.Name),
			ProjectID: c.ProjectID,
		}

		for _, app := range staticPlan {
			domain := strings.SplitN(app.App.URL, "/", 2)[0]

			certsCreate = append(certsCreate, &gcp.ManagedSSLCreate{
				Name:      gcp.ID(pctx.Env().ProjectName(), c.ProjectID, domain),
				Domain:    domain,
				ProjectID: c.ProjectID,
			})
		}

		for _, app := range static {
			negC := &gcp.ServerlessNEGCreate{
				Name:      app.ProxyCloudRun.Planned.Name,
				Region:    app.ProxyCloudRun.Planned.Region,
				ProjectID: app.ProxyCloudRun.Planned.ProjectID,
				CloudRun:  app.ProxyCloudRun.Planned.Name,
			}

			negCreate = append(negCreate, negC)

			backendServicesCreate = append(backendServicesCreate, &gcp.BackendServiceCreate{
				Name:      negC.Name,
				ProjectID: negC.ProjectID,
				NEG:       negC.ID(),
				Options: &gcp.BackendServiceOptions{
					CDN: gcp.BackendServiceOptionsCDN{
						Enabled: true,
					},
				},
			})
		}
	}

	// Plan Address.
	err := plan.PlanObject(pctx, o, "Address", addressCreate, verify)
	if err != nil {
		return err
	}

	// Plan Certificates.
	err = plan.PlanObjectList(pctx, o, "Certificates", certsCreate, verify)
	if err != nil {
		return err
	}

	// Plan NEG.
	err = plan.PlanObjectList(pctx, o, "NetworkEndpointGroups", negCreate, verify)
	if err != nil {
		return err
	}

	// Plan Backend Services.
	err = plan.PlanObjectList(pctx, o, "BackendServices", backendServicesCreate, verify)
	if err != nil {
		return err
	}

	return nil
}
