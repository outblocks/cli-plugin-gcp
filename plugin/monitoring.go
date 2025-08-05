package plugin

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func prepareRegistry(reg *registry.Registry, data []byte) error {
	gcp.RegisterTypes(reg)

	return reg.Load(data)
}

func (p *Plugin) registerMonitoring(reg *registry.Registry, data *apiv1.MonitoringData) error {
	var (
		checks   []*gcp.UptimeCheckConfig
		channels []*gcp.NotificationChannel
	)

	for _, target := range data.Targets {
		check := &gcp.UptimeCheckConfig{
			DisplayName: fields.String(fmt.Sprintf("Outblock Uptime Check for %s/%s: %s",
				p.env.Env(), p.env.ProjectName(), target.Url)),
			ProjectID: fields.String(p.settings.ProjectID),
			URL:       fields.String(target.Url),
			Frequency: fields.Int(int(target.Frequency)),
		}

		checks = append(checks, check)

		_, err := reg.RegisterPluginResource("uptime check", target.Url, check)
		if err != nil {
			return err
		}
	}

	for _, ch := range data.Channels {
		labels := make(map[string]fields.Field)

		var chID string

		switch ch.Type {
		case "slack":
			obj, err := types.NewMonitoringChannelSlack(ch.Properties.AsMap())
			if err != nil {
				return err
			}

			labels["channel_name"] = fields.String(obj.Channel)
			labels["auth_token"] = fields.String(obj.Token)
			chID = fmt.Sprintf("slack:%s", obj.Channel)

		case "email":
			obj, err := types.NewMonitoringChannelEmail(ch.Properties.AsMap())
			if err != nil {
				return err
			}

			labels["email_address"] = fields.String(obj.Email)
			chID = fmt.Sprintf("email:%s", obj.Email)
		default:
			continue
		}

		channel := &gcp.NotificationChannel{
			DisplayName: fields.String(fmt.Sprintf("Outblocks Notification for %s/%s: %s",
				p.env.Env(), p.env.ProjectName(), chID)),
			ProjectID: fields.String(p.settings.ProjectID),
			Type:      fields.String(ch.Type),
			Labels:    fields.Map(labels),
		}

		channels = append(channels, channel)

		_, err := reg.RegisterPluginResource("notification channel", chID, channel)
		if err != nil {
			return err
		}
	}

	if len(channels) != 0 {
		chIDs := make([]fields.Field, len(channels))
		for i, ch := range channels {
			chIDs[i] = ch.ID.Input()
		}

		for _, t := range checks {
			_, err := reg.RegisterPluginResource("uptime alert", t.URL.Wanted(), &gcp.UptimeAlertPolicy{
				DisplayName: fields.String(fmt.Sprintf("Outblocks Uptime Alert for %s/%s: %s",
					p.env.Env(), p.env.ProjectName(), t.URL.Wanted())),
				ProjectID:              t.ProjectID,
				CheckID:                t.ID.Input(),
				NotificationChannelIDs: fields.Array(chIDs),
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (p *Plugin) PlanMonitoring(ctx context.Context, reg *registry.Registry, r *apiv1.PlanMonitoringRequest) (*apiv1.PlanMonitoringResponse, error) {
	if r.State.Other == nil {
		r.State.Other = make(map[string][]byte)
	}

	reg = reg.Partition("monitoring")
	pctx := p.PluginContext()
	state := r.State
	monitoring := r.Data

	err := prepareRegistry(reg, r.State.Registry)
	if err != nil {
		return nil, err
	}

	// Register monitoring objects.
	err = p.registerMonitoring(reg, monitoring)
	if err != nil {
		return nil, err
	}

	// Process registry.
	diff, err := reg.ProcessAndDiff(ctx, pctx)
	if err != nil {
		return nil, err
	}

	data, err := reg.Dump()
	if err != nil {
		return nil, err
	}

	r.State.Registry = data

	return &apiv1.PlanMonitoringResponse{
		Plan: &apiv1.Plan{
			Actions: registry.PlanActionFromDiff(diff),
		},
		State: state,
	}, nil
}

func (p *Plugin) ApplyMonitoring(r *apiv1.ApplyMonitoringRequest, reg *registry.Registry, stream apiv1.MonitoringPluginService_ApplyMonitoringServer) error {
	reg = reg.Partition("monitoring")
	ctx := stream.Context()
	pctx := p.PluginContext()
	monitoring := r.Data

	err := prepareRegistry(reg, r.State.Registry)
	if err != nil {
		return err
	}

	// Register monitoring objects.
	err = p.registerMonitoring(reg, monitoring)
	if err != nil {
		return err
	}

	// Process registry.
	diff, err := reg.ProcessAndDiff(ctx, pctx)
	if err != nil {
		return err
	}

	err = reg.Apply(ctx, pctx, diff, plugin_go.DefaultRegistryApplyMonitoringCallback(stream))

	data, saveErr := reg.Dump()
	if err == nil {
		err = saveErr
	}

	r.State.Registry = data

	_ = stream.Send(&apiv1.ApplyMonitoringResponse{
		Response: &apiv1.ApplyMonitoringResponse_Done{
			Done: &apiv1.ApplyMonitoringDoneResponse{
				State: r.State,
			},
		},
	})

	return err
}
