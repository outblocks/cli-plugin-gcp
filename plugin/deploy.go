package plugin

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
)

func (p *Plugin) Plan(ctx context.Context, reg *registry.Registry, r *apiv1.PlanRequest) (*apiv1.PlanResponse, error) {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.State, r.Domains, reg, r.Destroy, false)
	if err != nil {
		return nil, err
	}

	deployPlan, err := a.Plan(ctx, r.Apps, r.Dependencies)
	if err != nil {
		return nil, err
	}

	return &apiv1.PlanResponse{
		Plan: deployPlan,

		State:            a.State,
		AppStates:        a.AppStates,
		DependencyStates: a.DependencyStates,
		DnsRecords:       a.DNSRecords,
	}, nil
}

func (p *Plugin) Apply(r *apiv1.ApplyRequest, reg *registry.Registry, stream apiv1.DeployPluginService_ApplyServer) error {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.State, r.Domains, reg, r.Destroy, false)
	if err != nil {
		return err
	}

	err = a.Apply(stream.Context(), r.Apps, r.Dependencies, plugin_go.DefaultRegistryApplyCallback(stream))

	_ = stream.Send(&apiv1.ApplyResponse{
		Response: &apiv1.ApplyResponse_Done{
			Done: &apiv1.ApplyDoneResponse{
				State:            a.State,
				AppStates:        a.AppStates,
				DependencyStates: a.DependencyStates,
				DnsRecords:       a.DNSRecords,
			},
		},
	})

	return err
}
