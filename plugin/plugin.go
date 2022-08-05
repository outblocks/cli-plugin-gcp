package plugin

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	plugin "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/env"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"golang.org/x/oauth2/google"
)

type Plugin struct {
	log     log.Logger
	env     env.Enver
	hostCli apiv1.HostServiceClient

	gcred       *google.Credentials
	settings    config.Settings
	apisEnabled map[string]struct{}
}

func NewPlugin() *Plugin {
	return &Plugin{
		apisEnabled: make(map[string]struct{}),
	}
}

func (p *Plugin) PluginContext() *config.PluginContext {
	return config.NewPluginContext(p.env, p.gcred, &p.settings)
}

func (p *Plugin) ensureAPI(ctx context.Context, api ...string) error {
	for _, a := range api {
		if _, ok := p.apisEnabled[a]; ok {
			continue
		}

		reqAPI := gcp.APIService{
			ProjectNumber: fields.Int(int(p.settings.ProjectNumber)),
			Name:          fields.String(a),
		}

		err := reqAPI.Create(ctx, p.PluginContext())
		if err != nil {
			return fmt.Errorf("error enabling required apis: %w", err)
		}

		p.apisEnabled[a] = struct{}{}
	}

	return nil
}

func (p *Plugin) runAndEnsureAPI(ctx context.Context, f func() error) error {
	apisEnabled := make(map[string]struct{})

	for {
		err := f()

		missingAPI := gcp.ErrExtractMissingAPI(err)
		if missingAPI != "" {
			if _, ok := apisEnabled[missingAPI]; ok {
				return err
			}

			apisEnabled[missingAPI] = struct{}{}

			err = p.ensureAPI(ctx, missingAPI)
			if err != nil {
				return err
			}

			continue
		}

		return err
	}
}

var (
	_ plugin.LockingPluginHandler    = (*Plugin)(nil)
	_ plugin.StatePluginHandler      = (*Plugin)(nil)
	_ plugin.DeployPluginHandler     = (*Plugin)(nil)
	_ plugin.CommandPluginHandler    = (*Plugin)(nil)
	_ plugin.LogsPluginHandler       = (*Plugin)(nil)
	_ plugin.SecretPluginHandler     = (*Plugin)(nil)
	_ plugin.MonitoringPluginHandler = (*Plugin)(nil)
)
