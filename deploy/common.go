package deploy

import (
	"context"

	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/types"
	"google.golang.org/api/compute/v1"
)

const (
	CommonLoadBalancer = "load_balancer"
)

type Common struct {
	LoadBalancer *LoadBalancer `json:"load_balancer" mapstructure:"load_balancer"`
}

func NewCommon() *Common {
	return &Common{
		LoadBalancer: NewLoadBalancer(),
	}
}

type CommonCreate struct {
	Name      string
	ProjectID string
	Region    string
}

func (o *Common) Decode(in interface{}) error {
	err := mapstructure.Decode(in, o)
	if err != nil {
		return err
	}

	return nil
}

func (o *Common) Plan(ctx context.Context, cli *compute.Service, e env.Enver, c *CommonCreate, static []*StaticApp, staticPlan []*types.AppPlan, verify bool) (actions map[string]*types.PlanAction, err error) {
	var lbCreate *LoadBalancerCreate

	if len(static) == 0 {
		c = nil
	}

	if c != nil {
		lbCreate = &LoadBalancerCreate{
			Name:      c.Name,
			ProjectID: c.ProjectID,
			Region:    c.Region,
		}
	}

	actions, err = o.LoadBalancer.Plan(ctx, cli, e, lbCreate,
		static, staticPlan,
		verify)
	if err != nil {
		return nil, err
	}

	return actions, nil
}

func (o *Common) Apply(ctx context.Context, cli *compute.Service, actions map[string]*types.PlanAction, callback func(obj, desc string, progress, total int)) error {
	return o.LoadBalancer.Apply(ctx, cli, actions, callback)
}
