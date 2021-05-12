package plugin

import (
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
)

type Plugin struct {
	log      log.Logger
	env      env.Enver
	Settings struct {
		Project string
		Region  string
	}
}

func NewPlugin(log log.Logger, env env.Enver) *Plugin {
	return &Plugin{
		log: log,
		env: env,
	}
}

func (p *Plugin) Handler() *plugin_go.ReqHandler {
	return &plugin_go.ReqHandler{
		Init:             p.Init,
		Start:            p.Start,
		Plan:             p.Plan,
		AppleInteractive: p.ApplyInteractive,
	}
}
