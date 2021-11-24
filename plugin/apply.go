package plugin

import (
	"github.com/outblocks/cli-plugin-gcp/actions"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
)

func (p *Plugin) Apply(r *apiv1.ApplyRequest, reg *registry.Registry, stream apiv1.DeployPluginService_ApplyServer) error {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.State, reg, r.Destroy, false)
	if err != nil {
		return err
	}

	cb := func(a *apiv1.ApplyAction) {
		_ = stream.Send(&apiv1.ApplyResponse{
			Response: &apiv1.ApplyResponse_Action{
				Action: &apiv1.ApplyActionResponse{
					Actions: []*apiv1.ApplyAction{a},
				},
			},
		})
	}

	err = a.Apply(stream.Context(), r.Apps, r.Dependencies, cb)

	_ = stream.Send(&apiv1.ApplyResponse{
		Response: &apiv1.ApplyResponse_Done{
			Done: &apiv1.ApplyDoneResponse{
				State:            a.State,
				AppStates:        a.AppStates,
				DependencyStates: a.DependencyStates,
			},
		},
	})

	return err
}
