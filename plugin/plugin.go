package plugin

import (
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
	"golang.org/x/oauth2/google"
)

type Plugin struct {
	log log.Logger
	env env.Enver

	gcred    *google.Credentials
	Settings config.Settings
}

func NewPlugin(logger log.Logger, enver env.Enver) *Plugin {
	return &Plugin{
		log: logger,
		env: enver,
	}
}

func (p *Plugin) Handler() *plugin_go.ReqHandler {
	return &plugin_go.ReqHandler{
		ProjectInitInteractive: p.ProjectInitInteractive,
		StartInteractive:       p.StartInteractive,
		GetState:               p.GetState,
		SaveState:              p.SaveState,
		ReleaseStateLock:       p.ReleaseStateLock,
		AcquireLocks:           p.AcquireLocks,
		ReleaseLocks:           p.ReleaseLocks,
		Plan:                   p.Plan,
		ApplyInteractive:       p.ApplyInteractive,

		Options: plugin_go.ReqHandlerOptions{
			RegistryAllowDuplicates: true,
		},
	}
}

func (p *Plugin) PluginContext() *config.PluginContext {
	return config.NewPluginContext(p.env, p.gcred, &p.Settings)
}
