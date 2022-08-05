package gcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/genproto/googleapis/monitoring/v3"
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

func (o *UptimeAlertPolicy) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringAlertPolicyClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	id := o.ID.Current()

	if id == "" {
		return nil
	}

	obj, err := cli.GetAlertPolicy(ctx, &monitoring.GetAlertPolicyRequest{
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

	channels := make([]interface{}, len(obj.NotificationChannels))
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

func (o *UptimeAlertPolicy) createAlertPolicy(update bool) *monitoring.AlertPolicy {
	displayName := o.DisplayName.Wanted()
	checkID := o.CheckID.Wanted()
	channels := o.NotificationChannelIDs.Wanted()
	channelsStr := make([]string, len(channels))

	for i, v := range channels {
		channelsStr[i] = v.(string)
	}

	cfg := &monitoring.AlertPolicy{
		DisplayName: displayName,
		Conditions: []*monitoring.AlertPolicy_Condition{
			{
				DisplayName: "uptime check",
				Condition: &monitoring.AlertPolicy_Condition_ConditionThreshold{
					ConditionThreshold: &monitoring.AlertPolicy_Condition_MetricThreshold{
						Filter:         fmt.Sprintf("resource.type = \"uptime_url\" AND metric.type = \"monitoring.googleapis.com/uptime_check/check_passed\" AND metric.labels.check_id = \"%s\"", checkID),
						Duration:       durationpb.New(60 * time.Second),
						Comparison:     monitoring.ComparisonType_COMPARISON_GT,
						ThresholdValue: 2,

						Aggregations: []*monitoring.Aggregation{
							{
								AlignmentPeriod:    durationpb.New(1200 * time.Second),
								CrossSeriesReducer: monitoring.Aggregation_REDUCE_COUNT_FALSE,
								PerSeriesAligner:   monitoring.Aggregation_ALIGN_NEXT_OLDER,
								GroupByFields:      []string{"resource.*"},
							},
						},
					},
				},
			},
		},
		Combiner:             monitoring.AlertPolicy_OR,
		NotificationChannels: channelsStr,
	}

	if update {
		cfg.Name = o.ID.Current()
	}

	return cfg
}

func (o *UptimeAlertPolicy) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringAlertPolicyClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()

	obj, err := cli.CreateAlertPolicy(ctx, &monitoring.CreateAlertPolicyRequest{
		Name:        fmt.Sprintf("projects/%s", projectID),
		AlertPolicy: o.createAlertPolicy(false),
	})
	if err != nil {
		return err
	}

	o.ID.SetCurrent(obj.Name)

	return err
}

func (o *UptimeAlertPolicy) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringAlertPolicyClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.UpdateAlertPolicy(ctx, &monitoring.UpdateAlertPolicyRequest{
		AlertPolicy: o.createAlertPolicy(true),
	})

	return err
}

func (o *UptimeAlertPolicy) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringAlertPolicyClient(ctx)
	if err != nil {
		return err
	}

	err = cli.DeleteAlertPolicy(ctx, &monitoring.DeleteAlertPolicyRequest{
		Name: o.ID.Current(),
	})

	return err
}
