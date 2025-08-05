package gcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/protobuf/types/known/durationpb"
)

type UptimeAlertPolicy struct {
	registry.ResourceBase

	ID                     fields.StringOutputField
	DisplayName            fields.StringInputField `default:"Outblocks Notification Channel"`
	ProjectID              fields.StringInputField `state:"force_new"`
	CheckID                fields.StringInputField
	NotificationChannelIDs fields.ArrayInputField
}

func (o *UptimeAlertPolicy) ReferenceID() string {
	return o.ID.Current()
}

func (o *UptimeAlertPolicy) GetName() string {
	return fields.VerboseString(o.DisplayName)
}

func (o *UptimeAlertPolicy) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringAlertPolicyClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	id := o.ID.Current()

	if id == "" {
		return nil
	}

	obj, err := cli.GetAlertPolicy(ctx, &monitoringpb.GetAlertPolicyRequest{
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

	channels := make([]any, len(obj.NotificationChannels))
	for i, v := range obj.NotificationChannels {
		channels[i] = v
	}

	o.NotificationChannelIDs.SetCurrent(channels)

	o.CheckID.UnsetCurrent()

	if len(obj.Conditions) == 1 {
		s := strings.Split(obj.Conditions[0].GetConditionThreshold().Filter, "metric.labels.check_id = \"")
		if len(s) != 2 {
			return nil
		}

		s = strings.Split(s[1], "\"")

		o.CheckID.SetCurrent(s[0])
	}

	return nil
}

func (o *UptimeAlertPolicy) createAlertPolicy(update bool) *monitoringpb.AlertPolicy {
	displayName := o.DisplayName.Wanted()
	checkID := o.CheckID.Wanted()
	channels := o.NotificationChannelIDs.Wanted()
	channelsStr := make([]string, len(channels))

	for i, v := range channels {
		channelsStr[i] = v.(string) //nolint:errcheck
	}

	cfg := &monitoringpb.AlertPolicy{
		DisplayName: displayName,
		Conditions: []*monitoringpb.AlertPolicy_Condition{
			{
				DisplayName: "uptime check",
				Condition: &monitoringpb.AlertPolicy_Condition_ConditionThreshold{
					ConditionThreshold: &monitoringpb.AlertPolicy_Condition_MetricThreshold{
						Filter:         fmt.Sprintf("resource.type = \"uptime_url\" AND metric.type = \"monitoringpb.googleapis.com/uptime_check/check_passed\" AND metric.labels.check_id = \"%s\"", checkID), //nolint:gocritic
						Duration:       durationpb.New(60 * time.Second),
						Comparison:     monitoringpb.ComparisonType_COMPARISON_GT,
						ThresholdValue: 2,

						Aggregations: []*monitoringpb.Aggregation{
							{
								AlignmentPeriod:    durationpb.New(1200 * time.Second),
								CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_COUNT_FALSE,
								PerSeriesAligner:   monitoringpb.Aggregation_ALIGN_NEXT_OLDER,
								GroupByFields:      []string{"resource.*"},
							},
						},
					},
				},
			},
		},
		Combiner:             monitoringpb.AlertPolicy_OR,
		NotificationChannels: channelsStr,
	}

	if update {
		cfg.Name = o.ID.Current()
	}

	return cfg
}

func (o *UptimeAlertPolicy) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringAlertPolicyClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()

	obj, err := cli.CreateAlertPolicy(ctx, &monitoringpb.CreateAlertPolicyRequest{
		Name:        fmt.Sprintf("projects/%s", projectID),
		AlertPolicy: o.createAlertPolicy(false),
	})
	if err != nil {
		return err
	}

	o.ID.SetCurrent(obj.Name)

	return err
}

func (o *UptimeAlertPolicy) Update(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringAlertPolicyClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.UpdateAlertPolicy(ctx, &monitoringpb.UpdateAlertPolicyRequest{
		AlertPolicy: o.createAlertPolicy(true),
	})

	return err
}

func (o *UptimeAlertPolicy) Delete(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringAlertPolicyClient(ctx)
	if err != nil {
		return err
	}

	err = cli.DeleteAlertPolicy(ctx, &monitoringpb.DeleteAlertPolicyRequest{
		Name: o.ID.Current(),
	})

	return err
}
