package plugin

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/actions"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
)

func (p *Plugin) Plan(ctx context.Context, reg *registry.Registry, r *apiv1.PlanRequest) (*apiv1.PlanResponse, error) {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.PluginState, r.Domains, reg, r.Destroy, false)
	if err != nil {
		return nil, err
	}

	deployPlan, err := a.Plan(ctx, r.Apps, r.Dependencies)
	if err != nil {
		return nil, err
	}

	return &apiv1.PlanResponse{
		Deploy: deployPlan,

		State:            a.State,
		AppStates:        a.AppStates,
		DependencyStates: a.DependencyStates,
		DnsRecords:       a.DNSRecords,
	}, nil
}
