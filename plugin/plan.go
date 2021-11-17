package plugin

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/registry"
)

func (p *Plugin) Plan(ctx context.Context, r *plugin_go.PlanRequest, reg *registry.Registry) (plugin_go.Response, error) {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.PluginState, reg, r.Destroy, false)
	if err != nil {
		return nil, err
	}

	deployPlan, err := a.Plan(ctx, r.Apps, r.Dependencies)
	if err != nil {
		return nil, err
	}

	return &plugin_go.PlanResponse{
		DeployPlan: deployPlan,

		PluginState:      a.State,
		AppStates:        a.AppStates,
		DependencyStates: a.DependencyStates,
	}, nil
}
