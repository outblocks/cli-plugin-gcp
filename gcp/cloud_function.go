package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/cloudfunctions/v1"
)

type CloudFunction struct {
	registry.ResourceBase

	Name         fields.StringInputField `state:"force_new"`
	ProjectID    fields.StringInputField `state:"force_new"`
	Region       fields.StringInputField `state:"force_new"`
	Entrypoint   fields.StringInputField
	Runtime      fields.StringInputField
	SourceBucket fields.StringInputField
	SourceObject fields.StringInputField
	IsPublic     fields.BoolInputField

	URL           fields.StringOutputField
	Ready         fields.BoolOutputField
	StatusMessage fields.StringOutputField

	MinScale       fields.IntInputField `default:"0"`
	MaxScale       fields.IntInputField `default:"3000"`
	MemoryLimit    fields.IntInputField `default:"128"`
	TimeoutSeconds fields.IntInputField `default:"300"`
	EnvVars        fields.MapInputField
	Ingress        fields.StringInputField `default:"ALLOW_ALL" state:"force_new"` // options: ALLOW_INTERNAL_AND_GCLB, ALLOW_INTERNAL_ONLY
}

func (o *CloudFunction) ReferenceID() string {
	return fields.GenerateID("projects/%s/locations/%s/functions/%s", o.ProjectID, o.Region, o.Name)
}

func (o *CloudFunction) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *CloudFunction) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	projectID := o.ProjectID.Any()
	region := o.Region.Any()
	name := o.Name.Any()

	cli, err := pctx.GCPCloudFunctionsClient(ctx)
	if err != nil {
		return err
	}

	cf, err := getCloudFunction(cli, projectID, region, name)
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	}

	if err != nil {
		return fmt.Errorf("error fetching cloud function status: %w", err)
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)
	o.Region.SetCurrent(region)
	o.Entrypoint.SetCurrent(cf.EntryPoint)
	o.Runtime.SetCurrent(cf.Runtime)

	source := cf.SourceArchiveUrl

	u, err := url.Parse(source)
	if err != nil {
		return fmt.Errorf("error parsing source archive url: %w", err)
	}

	o.SourceBucket.SetCurrent(u.Host)
	o.SourceObject.SetCurrent(u.Path[1:])

	o.setCurrentStatusInfo(cf)
	o.MinScale.SetCurrent(int(cf.MinInstances))
	o.MaxScale.SetCurrent(int(cf.MaxInstances))
	o.MemoryLimit.SetCurrent(int(cf.AvailableMemoryMb))

	t, _ := strconv.Atoi(strings.TrimSuffix(cf.Timeout, "s"))
	o.TimeoutSeconds.SetCurrent(t)

	envVars := make(map[string]any)

	for k, v := range cf.EnvironmentVariables {
		envVars[k] = v
	}

	o.EnvVars.SetCurrent(envVars)
	o.Ingress.SetCurrent(cf.IngressSettings)

	policy, err := cli.Projects.Locations.Functions.GetIamPolicy(cf.Name).Do()
	if err != nil {
		return fmt.Errorf("error fetching cloud function policy: %w", err)
	}

	isPublic := false

	for _, b := range policy.Bindings {
		if b.Role == "roles/cloudfunctions.invoker" {
			isPublic = plugin_util.StringSliceContains(b.Members, ACLAllUsers)
			break
		}
	}

	o.IsPublic.SetCurrent(isPublic)

	return nil
}

func (o *CloudFunction) setCurrentStatusInfo(cf *cloudfunctions.CloudFunction) {
	o.Ready.SetCurrent(cf.Status == CloudFunctionReady)

	if cf.Status == CloudFunctionOffline {
		o.StatusMessage.SetCurrent(fmt.Sprintf("Function failed on loading user code. This is likely due to a bug in the user code. Please examine your function logs to see the error cause: \nhttps://console.cloud.google.com/functions/details/%s/%s?project=%s&tab=logs", o.Region.Wanted(), o.Name.Wanted(), o.ProjectID.Wanted()))
	} else {
		o.StatusMessage.SetCurrent(fmt.Sprintf("Function failed to deploy: %s", cf.Status))
	}

	o.URL.SetCurrent(cf.HttpsTrigger.Url)
}

func (o *CloudFunction) wantedAPICloudFunction() *cloudfunctions.CloudFunction {
	envvarsIntf := o.EnvVars.Wanted()
	envvars := make(map[string]string, len(envvarsIntf))

	for k, v := range envvarsIntf {
		envvars[k] = v.(string) //nolint:errcheck
	}

	return &cloudfunctions.CloudFunction{
		Name:                      fmt.Sprintf("projects/%s/locations/%s/functions/%s", o.ProjectID.Wanted(), o.Region.Wanted(), o.Name.Wanted()),
		AvailableMemoryMb:         int64(o.MemoryLimit.Wanted()),
		BuildEnvironmentVariables: envvars,
		EnvironmentVariables:      envvars,
		EntryPoint:                o.Entrypoint.Wanted(),
		IngressSettings:           o.Ingress.Wanted(),
		MinInstances:              int64(o.MinScale.Wanted()),
		MaxInstances:              int64(o.MaxScale.Wanted()),
		Runtime:                   o.Runtime.Wanted(),
		HttpsTrigger:              &cloudfunctions.HttpsTrigger{},
		Timeout:                   fmt.Sprintf("%ds", o.TimeoutSeconds.Wanted()),
		SourceArchiveUrl:          fmt.Sprintf("gs://%s/%s", o.SourceBucket.Wanted(), o.SourceObject.Wanted()),
	}
}

func (o *CloudFunction) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	projectID := o.ProjectID.Wanted()
	region := o.Region.Wanted()
	name := o.Name.Wanted()

	cli, err := pctx.GCPCloudFunctionsClient(ctx)
	if err != nil {
		return err
	}

	op, err := createCloudFunction(cli, projectID, region, o.wantedAPICloudFunction())
	if err != nil {
		return err
	}

	opErr := waitForCloudFunctionsOperation(ctx, cli, op)

	cf, err := getCloudFunction(cli, projectID, region, name)
	if err != nil {
		if opErr != nil {
			return opErr
		}

		return err
	}

	o.setCurrentStatusInfo(cf)

	if !o.IsPublic.Wanted() {
		return nil
	}

	policy, err := cli.Projects.Locations.Functions.GetIamPolicy(cf.Name).Do()
	if err != nil {
		return fmt.Errorf("error fetching cloud function policy: %w", err)
	}

	policy.Bindings = append(policy.Bindings, &cloudfunctions.Binding{
		Members: []string{ACLAllUsers},
		Role:    "roles/cloudfunctions.invoker",
	})

	_, err = cli.Projects.Locations.Functions.SetIamPolicy(cf.Name, &cloudfunctions.SetIamPolicyRequest{
		Policy: policy,
	}).Do()
	if err != nil {
		return fmt.Errorf("error settings cloud function policy: %w", err)
	}

	return nil
}

func (o *CloudFunction) Update(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	projectID := o.ProjectID.Wanted()
	region := o.Region.Wanted()
	name := o.Name.Wanted()

	cli, err := pctx.GCPCloudFunctionsClient(ctx)
	if err != nil {
		return err
	}

	op, err := updateCloudFunction(cli, projectID, region, name, o.wantedAPICloudFunction())
	if err != nil {
		return err
	}

	opErr := waitForCloudFunctionsOperation(ctx, cli, op)

	cf, err := getCloudFunction(cli, projectID, region, name)
	if err != nil {
		if opErr != nil {
			return opErr
		}

		return err
	}

	o.setCurrentStatusInfo(cf)

	policy, err := cli.Projects.Locations.Functions.GetIamPolicy(cf.Name).Do()
	if err != nil {
		return fmt.Errorf("error fetching cloud function policy: %w", err)
	}

	var newBindings []*cloudfunctions.Binding

	added := false

	for _, b := range policy.Bindings {
		if b.Role == "roles/cloudfunctions.invoker" {
			if !o.IsPublic.Wanted() {
				if !plugin_util.StringSliceContains(b.Members, ACLAllUsers) {
					newBindings = append(newBindings, b)
					continue
				}

				// Remove member or whole binding if it's the only member.
				if len(b.Members) == 1 {
					continue
				}

				var newMembers []string

				for _, m := range b.Members {
					if m != ACLAllUsers {
						newMembers = append(newMembers, m)
					}
				}

				b.Members = newMembers
			} else if !plugin_util.StringSliceContains(b.Members, ACLAllUsers) {
				b.Members = append(b.Members, ACLAllUsers)
				added = true
			}
		}

		newBindings = append(newBindings, b)
	}

	if o.IsPublic.Wanted() && !added {
		newBindings = append(newBindings, &cloudfunctions.Binding{
			Members: []string{ACLAllUsers},
			Role:    "roles/cloudfunctions.invoker",
		})
	}

	policy.Bindings = newBindings

	_, err = cli.Projects.Locations.Functions.SetIamPolicy(cf.Name, &cloudfunctions.SetIamPolicyRequest{
		Policy: policy,
	}).Do()
	if err != nil {
		return fmt.Errorf("error settings cloud function policy: %w", err)
	}

	return nil
}

func (o *CloudFunction) Delete(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	projectID := o.ProjectID.Current()
	region := o.Region.Current()
	name := o.Name.Current()

	cli, err := pctx.GCPCloudFunctionsClient(ctx)
	if err != nil {
		return err
	}

	op, err := deleteCloudFunction(cli, projectID, region, name)
	if err != nil {
		return err
	}

	return waitForCloudFunctionsOperation(ctx, cli, op)
}

func createCloudFunction(cli *cloudfunctions.Service, project, region string, cf *cloudfunctions.CloudFunction) (*cloudfunctions.Operation, error) {
	return cli.Projects.Locations.Functions.Create(fmt.Sprintf("projects/%s/locations/%s", project, region), cf).Do()
}

func updateCloudFunction(cli *cloudfunctions.Service, project, region, name string, cf *cloudfunctions.CloudFunction) (*cloudfunctions.Operation, error) {
	return cli.Projects.Locations.Functions.Patch(fmt.Sprintf("projects/%s/locations/%s/functions/%s", project, region, name), cf).Do()
}

func getCloudFunction(cli *cloudfunctions.Service, project, region, name string) (*cloudfunctions.CloudFunction, error) {
	return cli.Projects.Locations.Functions.Get(fmt.Sprintf("projects/%s/locations/%s/functions/%s", project, region, name)).Do()
}

func deleteCloudFunction(cli *cloudfunctions.Service, project, region, name string) (*cloudfunctions.Operation, error) {
	return cli.Projects.Locations.Functions.Delete(fmt.Sprintf("projects/%s/locations/%s/functions/%s", project, region, name)).Do()
}

func waitForCloudFunctionsOperation(ctx context.Context, cli *cloudfunctions.Service, op *cloudfunctions.Operation) error {
	if op.Done {
		return nil
	}

	t := time.NewTicker(time.Second)
	defer t.Stop()

	var err error

	for {
		op, err = cli.Operations.Get(op.Name).Do()
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}

		if op.Done {
			if op.Error != nil {
				return errors.New(op.Error.Message)
			}

			return nil
		}
	}
}
