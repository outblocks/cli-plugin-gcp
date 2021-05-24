package plugin

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *Plugin) ApplyInteractive(ctx context.Context, r *plugin_go.ApplyRequest, in <-chan plugin_go.Request, out chan<- plugin_go.Response) error {
	p.log.Errorln("apply", r.DeployPlan, r.DNSPlan)

	a := actions.NewApply(ctx, &p.Settings, p.log, p.env, r.PluginMap, r.AppStates, r.DependencyStates)

	cb := func(a *types.ApplyAction) {
		out <- &plugin_go.ApplyResponse{
			Actions: []*types.ApplyAction{a},
		}
	}

	err := a.ApplyDeploy(r.DeployPlan, cb)
	if err != nil {
		return err
	}

	err = a.ApplyDNS(r.DNSPlan, cb)
	if err != nil {
		return err
	}

	out <- &plugin_go.ApplyDoneResponse{
		PluginMap:        a.PluginMap,
		AppStates:        a.AppStates,
		DependencyStates: a.DependencyStates,
	}

	close(out)

	return nil
}
