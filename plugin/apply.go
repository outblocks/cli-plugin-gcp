package plugin

import (
	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
)

func (p *Plugin) Apply(r *apiv1.ApplyRequest, reg *registry.Registry, stream apiv1.DeployPluginService_ApplyServer) error {
	a, err := actions.NewPlan(p.PluginContext(), p.log, r.PluginState, r.Domains, reg, r.Destroy, false)
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
