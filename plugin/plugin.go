package plugin

import (
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	plugin "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/env"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/log"
	"golang.org/x/oauth2/google"
)

type Plugin struct {
	log     log.Logger
	env     env.Enver
	hostCli apiv1.HostServiceClient

	gcred    *google.Credentials
	Settings config.Settings
}

func NewPlugin() *Plugin {
	return &Plugin{}
}

func (p *Plugin) PluginContext() *config.PluginContext {
	return config.NewPluginContext(p.env, p.gcred, &p.Settings)
}

var (
	_ plugin.LockingPluginHandler = (*Plugin)(nil)
	_ plugin.StatePluginHandler   = (*Plugin)(nil)
	_ plugin.DeployPluginHandler  = (*Plugin)(nil)
	_ plugin.CommandPluginHandler = (*Plugin)(nil)
)
