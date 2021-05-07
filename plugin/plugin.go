package plugin

import (
	comm "github.com/outblocks/outblocks-plugin-go"
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

func (p *Plugin) Handler() *comm.ReqHandler {
	return &comm.ReqHandler{
		Init:  p.Init,
		Start: p.Start,
	}
}
