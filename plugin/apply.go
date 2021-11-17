package plugin

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *Plugin) ApplyInteractive(ctx context.Context, r *plugin_go.ApplyRequest, reg *registry.Registry, stream *plugin_go.ReceiverStream) error {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.PluginState, reg, r.Destroy, false)
	if err != nil {
		return err
	}

	cb := func(a *types.ApplyAction) {
		_ = stream.Send(&plugin_go.ApplyResponse{
			Actions: []*types.ApplyAction{a},
		})
	}

	err = a.Apply(ctx, r.Apps, r.Dependencies, cb)

	_ = stream.Send(&plugin_go.ApplyDoneResponse{
		PluginState:      a.State,
		AppStates:        a.AppStates,
		DependencyStates: a.DependencyStates,
	})

	return err
}
