package gcp

import (
	"context"
	"fmt"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
)

type NotificationChannel struct {
	registry.ResourceBase

	ID          fields.StringOutputField
	DisplayName fields.StringInputField `default:"Outblocks Notification Channel"`
	ProjectID   fields.StringInputField `state:"force_new"`
	Type        fields.StringInputField
	Labels      fields.MapInputField
}

func (o *NotificationChannel) ReferenceID() string {
	return o.ID.Current()
}

func (o *NotificationChannel) GetName() string {
	return fields.VerboseString(o.DisplayName)
}

func (o *NotificationChannel) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringNotificationChannelClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	id := o.ID.Current()

	if id == "" {
		return nil
	}

	obj, err := cli.GetNotificationChannel(ctx, &monitoringpb.GetNotificationChannelRequest{
		Name: id,
	})
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.DisplayName.SetCurrent(obj.DisplayName)
	o.Type.SetCurrent(obj.Type)

	labels := make(map[string]any, len(obj.Labels))

	for k, v := range obj.Labels {
		labels[k] = v
	}

	o.Labels.SetCurrent(labels)

	return nil
}

func (o *NotificationChannel) createNotificationChannel(update bool) *monitoringpb.NotificationChannel {
	displayName := o.DisplayName.Wanted()
	typ := o.Type.Wanted()

	labels := o.Labels.Wanted()
	labelsMap := make(map[string]string, len(labels))

	for k, v := range labels {
		labelsMap[k] = v.(string) //nolint:errcheck
	}

	cfg := &monitoringpb.NotificationChannel{
		DisplayName: displayName,
		Type:        typ,
		Labels:      labelsMap,
	}

	if update {
		cfg.Name = o.ID.Current()
	}

	return cfg
}

func (o *NotificationChannel) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringNotificationChannelClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()

	obj, err := cli.CreateNotificationChannel(ctx, &monitoringpb.CreateNotificationChannelRequest{
		Name:                fmt.Sprintf("projects/%s", projectID),
		NotificationChannel: o.createNotificationChannel(false),
	})
	if err != nil {
		return err
	}

	o.ID.SetCurrent(obj.Name)

	return err
}

func (o *NotificationChannel) Update(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringNotificationChannelClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.UpdateNotificationChannel(ctx, &monitoringpb.UpdateNotificationChannelRequest{
		NotificationChannel: o.createNotificationChannel(true),
	})

	return err
}

func (o *NotificationChannel) Delete(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringNotificationChannelClient(ctx)
	if err != nil {
		return err
	}

	err = cli.DeleteNotificationChannel(ctx, &monitoringpb.DeleteNotificationChannelRequest{
		Name: o.ID.Current(),
	})

	return err
}
