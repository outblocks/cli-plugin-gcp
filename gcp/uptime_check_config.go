package gcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/genproto/googleapis/monitoring/v3"
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

func (o *UptimeCheckConfig) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringUptimeCheckClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	id := o.ID.Current()

	if id == "" {
		return nil
	}

	obj, err := cli.GetUptimeCheckConfig(ctx, &monitoring.GetUptimeCheckConfigRequest{
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

	wantedRegions := make(map[monitoring.UptimeCheckRegion]struct{})

	for _, reg := range o.Regions.Wanted() {
		r := stringToUptimeCheckRegion(reg.(string))

		if r == monitoring.UptimeCheckRegion_REGION_UNSPECIFIED {
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
		regs := make([]interface{}, len(obj.SelectedRegions))
		for i, r := range obj.SelectedRegions {
			regs[i] = r.String()
		}

		o.Regions.SetCurrent(regs)
	}

	return nil
}

func stringToUptimeCheckRegion(r string) monitoring.UptimeCheckRegion {
	switch strings.ToLower(r) {
	case "usa":
		return monitoring.UptimeCheckRegion_USA
	case "europe":
		return monitoring.UptimeCheckRegion_EUROPE
	case "south_america":
		return monitoring.UptimeCheckRegion_SOUTH_AMERICA
	case "asia":
		return monitoring.UptimeCheckRegion_ASIA_PACIFIC
	}

	return monitoring.UptimeCheckRegion_REGION_UNSPECIFIED
}

func (o *UptimeCheckConfig) createUptimeCheckConfig(update bool) *monitoring.UptimeCheckConfig {
	projectID := o.ProjectID.Wanted()
	displayName := o.DisplayName.Wanted()
	u, _ := url.Parse(o.URL.Wanted())
	freq := o.Frequency.Wanted()
	timeout := o.Timeout.Wanted()
	regions := o.Regions.Wanted()

	var selRegions []monitoring.UptimeCheckRegion

	for _, reg := range regions {
		r := stringToUptimeCheckRegion(reg.(string))
		if r == monitoring.UptimeCheckRegion_REGION_UNSPECIFIED {
			continue
		}

		selRegions = append(selRegions, r)
	}

	cfg := &monitoring.UptimeCheckConfig{
		DisplayName: displayName,
		Resource: &monitoring.UptimeCheckConfig_MonitoredResource{
			MonitoredResource: &monitoredres.MonitoredResource{
				Type: "uptime_url",
				Labels: map[string]string{
					"project_id": projectID,
					"host":       u.Hostname(),
				},
			},
		},
		CheckRequestType: &monitoring.UptimeCheckConfig_HttpCheck_{
			HttpCheck: &monitoring.UptimeCheckConfig_HttpCheck{
				RequestMethod: monitoring.UptimeCheckConfig_HttpCheck_GET,
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

func (o *UptimeCheckConfig) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringUptimeCheckClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()

	obj, err := cli.CreateUptimeCheckConfig(ctx, &monitoring.CreateUptimeCheckConfigRequest{
		Parent:            fmt.Sprintf("projects/%s", projectID),
		UptimeCheckConfig: o.createUptimeCheckConfig(false),
	})
	if err != nil {
		return err
	}

	o.ID.SetCurrent(obj.Name)

	return err
}

func (o *UptimeCheckConfig) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringUptimeCheckClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.UpdateUptimeCheckConfig(ctx, &monitoring.UpdateUptimeCheckConfigRequest{
		UptimeCheckConfig: o.createUptimeCheckConfig(true),
	})

	return err
}

func (o *UptimeCheckConfig) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPMonitoringUptimeCheckClient(ctx)
	if err != nil {
		return err
	}

	err = cli.DeleteUptimeCheckConfig(ctx, &monitoring.DeleteUptimeCheckConfigRequest{
		Name: o.ID.Current(),
	})

	return err
}
