package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/cloudscheduler/v1"
)

type CloudSchedulerJob struct {
	registry.ResourceBase

	Name        fields.StringInputField `state:"force_new"`
	ProjectID   fields.StringInputField `state:"force_new"`
	Region      fields.StringInputField `state:"force_new"`
	Schedule    fields.StringInputField `state:"force_new"`
	HTTPMethod  fields.StringInputField `state:"force_new" default:"GET"`
	HTTPURL     fields.StringInputField `state:"force_new"`
	HTTPHeaders fields.MapInputField    `state:"force_new"`
}

func (o *CloudSchedulerJob) ReferenceID() string {
	return fields.GenerateID("projects/%s/locations/%s/jobs/%s", o.ProjectID, o.Region, o.Name)
}

func (o *CloudSchedulerJob) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *CloudSchedulerJob) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	projectID := o.ProjectID.Any()
	region := o.Region.Any()
	name := o.Name.Any()
	id := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", projectID, region, name)

	cli, err := pctx.GCPCloudSchedulerClient(ctx)
	if err != nil {
		return err
	}

	job, err := cli.Projects.Locations.Jobs.Get(id).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	}

	if err != nil {
		return fmt.Errorf("error fetching cloud scheduler: %w", err)
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)
	o.Region.SetCurrent(region)
	o.Schedule.SetCurrent(job.Schedule)

	if job.HttpTarget == nil {
		o.HTTPMethod.UnsetCurrent()
		o.HTTPURL.UnsetCurrent()
		o.HTTPHeaders.UnsetCurrent()

		return nil
	}

	o.HTTPMethod.SetCurrent(job.HttpTarget.HttpMethod)
	o.HTTPURL.SetCurrent(job.HttpTarget.Uri)

	headers := make(map[string]any)

	for k, v := range job.HttpTarget.Headers {
		headers[k] = v
	}

	o.HTTPHeaders.SetCurrent(headers)

	return nil
}

func (o *CloudSchedulerJob) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	projectID := o.ProjectID.Wanted()
	region := o.Region.Wanted()
	parentID := fmt.Sprintf("projects/%s/locations/%s", projectID, region)

	cli, err := pctx.GCPCloudSchedulerClient(ctx)
	if err != nil {
		return err
	}

	headers := make(map[string]string)

	for k, v := range o.HTTPHeaders.Wanted() {
		headers[k] = v.(string) //nolint:errcheck
	}

	_, err = cli.Projects.Locations.Jobs.Create(parentID, o.makeJob()).Do()

	return err
}

func (o *CloudSchedulerJob) makeJob() *cloudscheduler.Job {
	headers := make(map[string]string)

	for k, v := range o.HTTPHeaders.Wanted() {
		headers[k] = v.(string) //nolint:errcheck
	}

	id := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", o.ProjectID.Wanted(), o.Region.Wanted(), o.Name.Wanted())

	return &cloudscheduler.Job{
		Name:     id,
		Schedule: o.Schedule.Wanted(),
		HttpTarget: &cloudscheduler.HttpTarget{
			HttpMethod: o.HTTPMethod.Wanted(),
			Uri:        o.HTTPURL.Wanted(),
			Headers:    headers,
		},
	}
}

func (o *CloudSchedulerJob) Update(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPCloudSchedulerClient(ctx)
	if err != nil {
		return err
	}

	headers := make(map[string]string)

	for k, v := range o.HTTPHeaders.Wanted() {
		headers[k] = v.(string) //nolint:errcheck
	}

	job := o.makeJob()

	_, err = cli.Projects.Locations.Jobs.Patch(job.Name, job).Do()

	return err
}

func (o *CloudSchedulerJob) Delete(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck
	projectID := o.ProjectID.Any()
	region := o.Region.Any()
	name := o.Name.Any()
	id := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", projectID, region, name)

	cli, err := pctx.GCPCloudSchedulerClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.Projects.Locations.Jobs.Delete(id).Do()

	return err
}
