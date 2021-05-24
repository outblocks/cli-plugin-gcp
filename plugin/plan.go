package plugin

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
)

func (p *Plugin) Plan(ctx context.Context, r *plugin_go.PlanRequest) (plugin_go.Response, error) {
	p.log.Errorln("plan", r.Apps, r.Dependencies)

	a := actions.NewPlan(ctx, &p.Settings, p.log, p.env, r.PluginState, r.Verify)

	deployPlan, err := a.PlanDeploy(r.Apps)
	if err != nil {
		return nil, err
	}

	return &plugin_go.PlanResponse{
		DeployPlan: deployPlan,
	}, nil
}
