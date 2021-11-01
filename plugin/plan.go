package plugin

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
)

func (p *Plugin) Plan(ctx context.Context, r *plugin_go.PlanRequest) (plugin_go.Response, error) {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.PluginMap, r.TargetApps, r.SkipApps, r.Verify, r.Destroy, false)
	if err != nil {
		return nil, err
	}

	deployPlan, err := a.Plan(ctx, r.Apps, r.Dependencies)
	if err != nil {
		return nil, err
	}

	return &plugin_go.PlanResponse{
		DeployPlan: deployPlan,

		PluginMap:        a.PluginMap,
		AppStates:        a.AppStates,
		DependencyStates: a.DependencyStates,
	}, nil
}
