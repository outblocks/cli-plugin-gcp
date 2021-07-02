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
		Init:             p.Init,
		Start:            p.Start,
		GetState:         p.GetState,
		SaveState:        p.SaveState,
		ReleaseLock:      p.ReleaseLock,
		Plan:             p.Plan,
		ApplyInteractive: p.ApplyInteractive,
	}
}

func (p *Plugin) PluginContext() *config.PluginContext {
	return config.NewPluginContext(p.env, p.gcred, &p.Settings)
}
