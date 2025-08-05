package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/protobuf/types/known/durationpb"
)

type UptimeCheckConfig struct {
	registry.ResourceBase

	ID          fields.StringOutputField
	DisplayName fields.StringInputField `default:"Outblocks Uptime Check"`
	ProjectID   fields.StringInputField `state:"force_new"`
	URL         fields.StringInputField
	Frequency   fields.IntInputField `default:"5"`
	Timeout     fields.IntInputField `default:"60"`
	Regions     fields.ArrayInputField
}

func (o *UptimeCheckConfig) ReferenceID() string {
	return o.ID.Current()
}

func (o *UptimeCheckConfig) GetName() string {
	return fields.VerboseString(o.DisplayName)
}

func (o *UptimeCheckConfig) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringUptimeCheckClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	id := o.ID.Current()

	if id == "" {
		return nil
	}

	obj, err := cli.GetUptimeCheckConfig(ctx, &monitoringpb.GetUptimeCheckConfigRequest{
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

	o.URL.UnsetCurrent()

	if obj.GetMonitoredResource().GetType() == "uptime_url" {
		u := "http://"

		if obj.GetHttpCheck().GetUseSsl() {
			u = "https://"
		}

		u = fmt.Sprintf("%s%s%s", u, obj.GetMonitoredResource().GetLabels()["host"], obj.GetHttpCheck().GetPath())
		o.URL.SetCurrent(u)
	}

	o.Frequency.SetCurrent(int(obj.Period.AsDuration() / time.Minute))
	o.Timeout.SetCurrent(int(obj.Timeout.AsDuration() / time.Second))

	wantedRegions := make(map[monitoringpb.UptimeCheckRegion]struct{})

	for _, reg := range o.Regions.Wanted() {
		r := stringToUptimeCheckRegion(reg.(string)) //nolint:errcheck

		if r == monitoringpb.UptimeCheckRegion_REGION_UNSPECIFIED {
			continue
		}

		wantedRegions[r] = struct{}{}
	}

	for _, reg := range obj.SelectedRegions {
		delete(wantedRegions, reg)
	}

	if len(wantedRegions) == 0 {
		o.Regions.SetCurrent(o.Regions.Wanted())
	} else {
		regs := make([]any, len(obj.SelectedRegions))
		for i, r := range obj.SelectedRegions {
			regs[i] = r.String()
		}

		o.Regions.SetCurrent(regs)
	}

	return nil
}

func stringToUptimeCheckRegion(r string) monitoringpb.UptimeCheckRegion {
	switch strings.ToLower(r) {
	case "usa":
		return monitoringpb.UptimeCheckRegion_USA
	case "europe":
		return monitoringpb.UptimeCheckRegion_EUROPE
	case "south_america":
		return monitoringpb.UptimeCheckRegion_SOUTH_AMERICA
	case "asia":
		return monitoringpb.UptimeCheckRegion_ASIA_PACIFIC
	}

	return monitoringpb.UptimeCheckRegion_REGION_UNSPECIFIED
}

func (o *UptimeCheckConfig) createUptimeCheckConfig(update bool) *monitoringpb.UptimeCheckConfig {
	projectID := o.ProjectID.Wanted()
	displayName := o.DisplayName.Wanted()
	u, _ := url.Parse(o.URL.Wanted())
	freq := o.Frequency.Wanted()
	timeout := o.Timeout.Wanted()
	regions := o.Regions.Wanted()

	var selRegions []monitoringpb.UptimeCheckRegion

	for _, reg := range regions {
		r := stringToUptimeCheckRegion(reg.(string)) //nolint:errcheck
		if r == monitoringpb.UptimeCheckRegion_REGION_UNSPECIFIED {
			continue
		}

		selRegions = append(selRegions, r)
	}

	cfg := &monitoringpb.UptimeCheckConfig{
		DisplayName: displayName,
		Resource: &monitoringpb.UptimeCheckConfig_MonitoredResource{
			MonitoredResource: &monitoredres.MonitoredResource{
				Type: "uptime_url",
				Labels: map[string]string{
					"project_id": projectID,
					"host":       u.Hostname(),
				},
			},
		},
		CheckRequestType: &monitoringpb.UptimeCheckConfig_HttpCheck_{
			HttpCheck: &monitoringpb.UptimeCheckConfig_HttpCheck{
				RequestMethod: monitoringpb.UptimeCheckConfig_HttpCheck_GET,
				UseSsl:        u.Scheme == "https",
				Path:          u.Path,
			},
		},
		Period:          durationpb.New(time.Duration(freq) * time.Minute),
		Timeout:         durationpb.New(time.Duration(timeout) * time.Second),
		SelectedRegions: selRegions,
	}

	if update {
		cfg.Name = o.ID.Current()
	}

	return cfg
}

func (o *UptimeCheckConfig) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringUptimeCheckClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()

	obj, err := cli.CreateUptimeCheckConfig(ctx, &monitoringpb.CreateUptimeCheckConfigRequest{
		Parent:            fmt.Sprintf("projects/%s", projectID),
		UptimeCheckConfig: o.createUptimeCheckConfig(false),
	})
	if err != nil {
		return err
	}

	o.ID.SetCurrent(obj.Name)

	return err
}

func (o *UptimeCheckConfig) Update(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringUptimeCheckClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.UpdateUptimeCheckConfig(ctx, &monitoringpb.UpdateUptimeCheckConfigRequest{
		UptimeCheckConfig: o.createUptimeCheckConfig(true),
	})

	return err
}

func (o *UptimeCheckConfig) Delete(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPMonitoringUptimeCheckClient(ctx)
	if err != nil {
		return err
	}

	err = cli.DeleteUptimeCheckConfig(ctx, &monitoringpb.DeleteUptimeCheckConfigRequest{
		Name: o.ID.Current(),
	})

	return err
}
