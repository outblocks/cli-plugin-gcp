package deploy

import (
	"context"
	"encoding/json"

	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/types"
	"google.golang.org/api/compute/v1"
)

const (
	LBAddress = "address"
)

type GCPManagedSSL struct {
	ID string `json:"id"`
}

type GCPNetworkEndpointGroup struct{}
type GCPBackendService struct{}
type GCPURLMap struct{}
type GCPTargetHTTPSProxy struct{}
type GCPTargetHTTPProxy struct{}
type GCPForwardingRules struct{}

type LoadBalancerCreate struct {
	Name      string
	ProjectID string
	Region    string
}

type LoadBalancer struct {
	Address      *GCPAddress      `json:"address"`
	Certificates []*GCPManagedSSL `json:"managed_ssl" mapstructure:"managed_ssl"`

	NetworkEndpointGroups []*GCPNetworkEndpointGroup `json:"network_endpoint_groups" mapstructure:"network_endpoint_groups"`
	BackendServices       []*GCPBackendService       `json:"backend_services" mapstructure:"backend_services"`
	URLMaps               []*GCPURLMap               `json:"url_maps" mapstructure:"url_maps"`
	TargetHTTPSProxies    []*GCPTargetHTTPSProxy     `json:"target_https_proxies" mapstructure:"target_https_proxies"`
	TargetHTTPProxies     []*GCPTargetHTTPProxy      `json:"target_http_proxies" mapstructure:"target_http_proxies"`
	ForwardingRules       []*GCPForwardingRules      `json:"forwarding_rules" mapstructure:"forwarding_rules"`
}

func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{
		Address: &GCPAddress{},
	}
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

func (o *LoadBalancer) Plan(ctx context.Context, cli *compute.Service, e env.Enver, c *LoadBalancerCreate, static []*StaticApp, staticPlan []*types.AppPlan, verify bool) (map[string]*types.PlanAction, error) {
	var (
		addressCreate *GCPAddressCreate
	)

	actions := make(map[string]*types.PlanAction)

	if c != nil {
		addressCreate = &GCPAddressCreate{
			Name:      ID(e.ProjectName(), c.ProjectID, c.Name),
			ProjectID: c.ProjectID,
		}
	}

	err := util.PlanObject(actions, LBAddress, o.Address.planner(ctx, cli, addressCreate, verify))
	if err != nil {
		return nil, err
	}

	return actions, nil
}

func (o *LoadBalancer) Apply(ctx context.Context, cli *compute.Service, actions map[string]*types.PlanAction, callback func(obj, desc string, progress, total int)) error {
	// Apply Address.
	err := util.ApplyObject(actions, LBAddress, callback, o.Address.applier(ctx, cli))
	if err != nil {
		return err
	}

	return nil
}
