package plugin

import (
	"github.com/outblocks/cli-plugin-gcp/actions"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
)

type Plugin struct {
	log log.Logger
	env env.Enver

	Settings actions.Settings
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
