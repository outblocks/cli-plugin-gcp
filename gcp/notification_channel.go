package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/genproto/googleapis/monitoring/v3"
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

func (o *NotificationChannel) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringNotificationChannelClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	id := o.ID.Current()

	if id == "" {
		return nil
	}

	obj, err := cli.GetNotificationChannel(ctx, &monitoring.GetNotificationChannelRequest{
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

	labels := make(map[string]interface{}, len(obj.Labels))

	for k, v := range obj.Labels {
		labels[k] = v
	}

	o.Labels.SetCurrent(labels)

	return nil
}

func (o *NotificationChannel) createNotificationChannel(update bool) *monitoring.NotificationChannel {
	displayName := o.DisplayName.Wanted()
	typ := o.Type.Wanted()

	labels := o.Labels.Wanted()
	labelsMap := make(map[string]string, len(labels))

	for k, v := range labels {
		labelsMap[k] = v.(string)
	}

	cfg := &monitoring.NotificationChannel{
		DisplayName: displayName,
		Type:        typ,
		Labels:      labelsMap,
	}

	if update {
		cfg.Name = o.ID.Current()
	}

	return cfg
}

func (o *NotificationChannel) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringNotificationChannelClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()

	obj, err := cli.CreateNotificationChannel(ctx, &monitoring.CreateNotificationChannelRequest{
		Name:                fmt.Sprintf("projects/%s", projectID),
		NotificationChannel: o.createNotificationChannel(false),
	})
	if err != nil {
		return err
	}

	o.ID.SetCurrent(obj.Name)

	return err
}

func (o *NotificationChannel) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringNotificationChannelClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.UpdateNotificationChannel(ctx, &monitoring.UpdateNotificationChannelRequest{
		NotificationChannel: o.createNotificationChannel(true),
	})

	return err
}

func (o *NotificationChannel) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringNotificationChannelClient(ctx)
	if err != nil {
		return err
	}

	err = cli.DeleteNotificationChannel(ctx, &monitoring.DeleteNotificationChannelRequest{
		Name: o.ID.Current(),
	})

	return err
}
