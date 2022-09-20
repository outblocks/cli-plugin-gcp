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
	Schedule    fields.StringInputField
	HTTPMethod  fields.StringInputField `default:"GET"`
	HTTPURL     fields.StringInputField
	HTTPHeaders fields.MapInputField
}

func (o *CloudSchedulerJob) ReferenceID() string {
	return fields.GenerateID("projects/%s/locations/%s/jobs/%s", o.ProjectID, o.Region, o.Name)
}

func (o *CloudSchedulerJob) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *CloudSchedulerJob) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

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

	headers := make(map[string]interface{})

	for k, v := range job.HttpTarget.Headers {
		headers[k] = v
	}

	o.HTTPHeaders.SetCurrent(headers)

	return nil
}

func (o *CloudSchedulerJob) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Wanted()
	region := o.Region.Wanted()
	parentID := fmt.Sprintf("projects/%s/locations/%s", projectID, region)

	cli, err := pctx.GCPCloudSchedulerClient(ctx)
	if err != nil {
		return err
	}

	headers := make(map[string]string)

	for k, v := range o.HTTPHeaders.Wanted() {
		headers[k] = v.(string)
	}

	_, err = cli.Projects.Locations.Jobs.Create(parentID, o.makeJob()).Do()

	return err
}

func (o *CloudSchedulerJob) makeJob() *cloudscheduler.Job {
	headers := make(map[string]string)

	for k, v := range o.HTTPHeaders.Wanted() {
		headers[k] = v.(string)
	}

	return &cloudscheduler.Job{
		Name:     o.Name.Wanted(),
		Schedule: o.Schedule.Wanted(),
		HttpTarget: &cloudscheduler.HttpTarget{
			HttpMethod: o.HTTPMethod.Wanted(),
			Uri:        o.HTTPURL.Wanted(),
			Headers:    headers,
		},
	}
}

func (o *CloudSchedulerJob) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Wanted()
	region := o.Region.Wanted()
	name := o.Name.Wanted()
	id := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", projectID, region, name)

	cli, err := pctx.GCPCloudSchedulerClient(ctx)
	if err != nil {
		return err
	}

	headers := make(map[string]string)

	for k, v := range o.HTTPHeaders.Wanted() {
		headers[k] = v.(string)
	}

	_, err = cli.Projects.Locations.Jobs.Patch(id, o.makeJob()).Do()

	return err
}

func (o *CloudSchedulerJob) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)
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
