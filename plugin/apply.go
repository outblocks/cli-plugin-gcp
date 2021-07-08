package plugin

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *Plugin) ApplyInteractive(ctx context.Context, r *plugin_go.ApplyRequest, in <-chan plugin_go.Request, out chan<- plugin_go.Response) error {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.PluginMap, r.AppStates, r.DependencyStates, false, r.Destroy, false)
	if err != nil {
		return err
	}

	cb := func(a *types.ApplyAction) {
		out <- &plugin_go.ApplyResponse{
			Actions: []*types.ApplyAction{a},
		}
	}

	err = a.Apply(ctx, r.Apps, cb)

	out <- &plugin_go.ApplyDoneResponse{
		PluginMap:        a.PluginMap,
		AppStates:        a.AppStates,
		DependencyStates: a.DependencyStates,
	}

	return err
}
