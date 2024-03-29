package gcp

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/run/v1"
)

type CloudRun struct {
	registry.ResourceBase

	Name      fields.StringInputField `state:"force_new"`
	ProjectID fields.StringInputField `state:"force_new"`
	Region    fields.StringInputField `state:"force_new"`
	Command   fields.ArrayInputField
	Args      fields.ArrayInputField
	Image     fields.StringInputField
	IsPublic  fields.BoolInputField

	URL           fields.StringOutputField
	Ready         fields.BoolOutputField
	StatusMessage fields.StringOutputField

	CloudSQLInstances    fields.StringInputField
	MinScale             fields.IntInputField    `default:"0"`
	MaxScale             fields.IntInputField    `default:"100"`
	CPULimit             fields.StringInputField `default:"1000m"`
	MemoryLimit          fields.StringInputField `default:"128Mi"`
	ContainerConcurrency fields.IntInputField    `default:"250"`
	TimeoutSeconds       fields.IntInputField    `default:"300"`
	Port                 fields.IntInputField    `default:"80"`
	EnvVars              fields.MapInputField
	Ingress              fields.StringInputField `default:"all"`  // options: internal-and-cloud-load-balancing
	ExecutionEnvironment fields.StringInputField `default:"gen1"` // options: gen2
	CPUThrottling        fields.BoolInputField   `default:"true"`
}

func (o *CloudRun) ReferenceID() string {
	return fields.GenerateID("locations/%s/namespaces/%s/services/%s", o.Region, o.ProjectID, o.Name)
}

func (o *CloudRun) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *CloudRun) Read(ctx context.Context, meta interface{}) error { //nolint: gocyclo
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Any()
	region := o.Region.Any()
	name := o.Name.Any()

	cli, err := pctx.GCPRunClient(ctx, region)
	if err != nil {
		return err
	}

	svc, err := getRunService(cli, projectID, name)
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	}

	if err != nil {
		return fmt.Errorf("error fetching cloud run service: %w", err)
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)
	o.Region.SetCurrent(region)

	if svc.Spec == nil || svc.Spec.Template == nil || svc.Spec.Template.Metadata == nil || len(svc.Spec.Template.Spec.Containers) != 1 || svc.Spec.Template.Spec.Containers[0].Resources == nil || len(svc.Spec.Template.Spec.Containers[0].Ports) != 1 {
		o.Image.UnsetCurrent()
		o.URL.UnsetCurrent()
		o.CloudSQLInstances.UnsetCurrent()
		o.CPULimit.UnsetCurrent()
		o.MemoryLimit.UnsetCurrent()
		o.ContainerConcurrency.UnsetCurrent()
		o.TimeoutSeconds.UnsetCurrent()
		o.Port.UnsetCurrent()
		o.MinScale.UnsetCurrent()
		o.MaxScale.UnsetCurrent()
		o.EnvVars.UnsetCurrent()
		o.Ingress.UnsetCurrent()
		o.CPUThrottling.UnsetCurrent()
		o.ExecutionEnvironment.UnsetCurrent()

		return nil
	}

	for _, cond := range svc.Status.Conditions {
		if cond.Type != CloudRunReady {
			continue
		}

		o.Ready.SetCurrent(cond.Status == CloudRunStatusTrue)
		o.StatusMessage.SetCurrent(cond.Message)
	}

	args := make([]interface{}, len(svc.Spec.Template.Spec.Containers[0].Args))
	for i, v := range svc.Spec.Template.Spec.Containers[0].Args {
		args[i] = v
	}

	command := make([]interface{}, len(svc.Spec.Template.Spec.Containers[0].Command))
	for i, v := range svc.Spec.Template.Spec.Containers[0].Command {
		command[i] = v
	}

	o.Command.SetCurrent(command)
	o.Args.SetCurrent(args)
	o.Image.SetCurrent(svc.Spec.Template.Spec.Containers[0].Image)
	o.URL.SetCurrent(svc.Status.Url)
	o.CloudSQLInstances.SetCurrent(svc.Spec.Template.Metadata.Annotations["run.googleapis.com/cloudsql-instances"])
	o.CPULimit.SetCurrent(svc.Spec.Template.Spec.Containers[0].Resources.Limits["cpu"])
	o.MemoryLimit.SetCurrent(svc.Spec.Template.Spec.Containers[0].Resources.Limits["memory"])
	o.ContainerConcurrency.SetCurrent(int(svc.Spec.Template.Spec.ContainerConcurrency))
	o.TimeoutSeconds.SetCurrent(int(svc.Spec.Template.Spec.TimeoutSeconds))
	o.Port.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort))
	o.Ingress.SetCurrent(svc.Metadata.Annotations["run.googleapis.com/ingress"])
	o.CPUThrottling.SetCurrent(svc.Spec.Template.Metadata.Annotations["run.googleapis.com/cpu-throttling"] == "true")
	o.ExecutionEnvironment.SetCurrent(svc.Spec.Template.Metadata.Annotations["run.googleapis.com/execution-environment"])

	v, _ := strconv.Atoi(svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/minScale"])
	o.MinScale.SetCurrent(v)

	v, _ = strconv.Atoi(svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/maxScale"])
	o.MaxScale.SetCurrent(v)

	envVars := make(map[string]interface{})

	for _, e := range svc.Spec.Template.Spec.Containers[0].Env {
		envVars[e.Name] = e.Value
	}

	o.EnvVars.SetCurrent(envVars)

	pol, err := cli.Projects.Locations.Services.GetIamPolicy(fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, name)).Do()
	if err != nil && !ErrIs404(err) {
		return err
	}

	if err == nil && pol != nil && len(pol.Bindings) == 1 && len(pol.Bindings[0].Members) == 1 && pol.Bindings[0].Role == "roles/run.invoker" && pol.Bindings[0].Members[0] == ACLAllUsers {
		o.IsPublic.SetCurrent(true)
	} else {
		o.IsPublic.SetCurrent(false)
	}

	return nil
}

func (o *CloudRun) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Wanted()
	region := o.Region.Wanted()
	name := o.Name.Wanted()
	isPublic := o.IsPublic.Wanted()

	cli, err := pctx.GCPRunClient(ctx, region)
	if err != nil {
		return err
	}

	_, err = createRunService(cli, projectID, o.makeRunService())
	if err != nil {
		return err
	}

	svc, ready, msg, err := waitForRunServiceReady(ctx, cli, projectID, name)
	if err != nil {
		return err
	}

	o.Ready.SetCurrent(ready)
	o.StatusMessage.SetCurrent(msg)
	o.URL.SetCurrent(svc.Status.Url)

	return setRunServiceIAMPolicy(cli, projectID, region, name, isPublic)
}

func (o *CloudRun) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Wanted()
	region := o.Region.Wanted()
	name := o.Name.Wanted()

	cli, err := pctx.GCPRunClient(ctx, region)
	if err != nil {
		return err
	}

	_, err = updateRunService(cli, projectID, name, o.makeRunService())
	if err != nil {
		return err
	}

	svc, ready, msg, err := waitForRunServiceReady(ctx, cli, projectID, name)
	if err != nil {
		return err
	}

	o.Ready.SetCurrent(ready)
	o.StatusMessage.SetCurrent(msg)
	o.URL.SetCurrent(svc.Status.Url)

	if o.IsPublic.IsChanged() {
		return setRunServiceIAMPolicy(cli, projectID, region, name, o.IsPublic.Wanted())
	}

	return nil
}

func (o *CloudRun) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Current()
	region := o.Region.Current()
	name := o.Name.Current()

	cli, err := pctx.GCPRunClient(ctx, region)
	if err != nil {
		return err
	}

	_, err = deleteRunService(cli, projectID, name)
	if err != nil {
		return err
	}

	return waitForRunServiceDeleted(ctx, cli, projectID, name)
}

func (o *CloudRun) makeRunService() *run.Service {
	var envVars []*run.EnvVar
	for k, v := range o.EnvVars.Wanted() {
		envVars = append(envVars, &run.EnvVar{Name: k, Value: v.(string)})
	}

	command := o.Command.Wanted()
	commandStr := make([]string, len(command))

	for i, v := range command {
		commandStr[i] = v.(string)
	}

	args := o.Args.Wanted()
	argsStr := make([]string, len(args))

	for i, v := range args {
		argsStr[i] = v.(string)
	}

	cpuThrottling := "false"
	if o.CPUThrottling.Wanted() {
		cpuThrottling = "true"
	}

	svc := &run.Service{
		ApiVersion: "serving.knative.dev/v1",
		Kind:       "Service",
		Metadata: &run.ObjectMeta{
			Name: o.Name.Wanted(),
			Annotations: map[string]string{
				"run.googleapis.com/ingress": o.Ingress.Wanted(),
			},
		},
		Spec: &run.ServiceSpec{
			Template: &run.RevisionTemplate{
				Metadata: &run.ObjectMeta{
					Annotations: map[string]string{
						"run.googleapis.com/client-name":           "outblocks",
						"autoscaling.knative.dev/minScale":         strconv.Itoa(o.MinScale.Wanted()),
						"autoscaling.knative.dev/maxScale":         strconv.Itoa(o.MaxScale.Wanted()),
						"run.googleapis.com/cloudsql-instances":    o.CloudSQLInstances.Wanted(),
						"run.googleapis.com/execution-environment": o.ExecutionEnvironment.Wanted(),
						"run.googleapis.com/cpu-throttling":        cpuThrottling,
					},
				},
				Spec: &run.RevisionSpec{
					ContainerConcurrency: int64(o.ContainerConcurrency.Wanted()),
					TimeoutSeconds:       int64(o.TimeoutSeconds.Wanted()),
					Containers: []*run.Container{
						{
							Command: commandStr,
							Args:    argsStr,
							Image:   o.Image.Wanted(),
							Env:     envVars,
							Ports:   []*run.ContainerPort{{ContainerPort: int64(o.Port.Wanted())}},
							Resources: &run.ResourceRequirements{
								Limits: map[string]string{
									"cpu":    o.CPULimit.Wanted(),
									"memory": o.MemoryLimit.Wanted(),
								},
							},
						},
					},
				},
			},
			Traffic: []*run.TrafficTarget{{Percent: 100, LatestRevision: true}},
		},
	}

	return svc
}

func createRunService(cli *run.APIService, project string, svc *run.Service) (*run.Service, error) {
	return cli.Namespaces.Services.Create(fmt.Sprintf("namespaces/%s", project), svc).Do()
}

func getRunService(cli *run.APIService, project, name string) (*run.Service, error) {
	return cli.Namespaces.Services.Get(fmt.Sprintf("namespaces/%s/services/%s", project, name)).Do()
}

func deleteRunService(cli *run.APIService, project, name string) (*run.Status, error) {
	return cli.Namespaces.Services.Delete(fmt.Sprintf("namespaces/%s/services/%s", project, name)).Do()
}

func updateRunService(cli *run.APIService, project, name string, svc *run.Service) (*run.Service, error) {
	return cli.Namespaces.Services.ReplaceService(fmt.Sprintf("namespaces/%s/services/%s", project, name), svc).Do()
}

func setRunServiceIAMPolicy(cli *run.APIService, project, region, name string, public bool) error {
	var policy *run.Policy

	if public {
		policy = &run.Policy{Bindings: []*run.Binding{{
			Members: []string{ACLAllUsers},
			Role:    "roles/run.invoker",
		}}}
	}

	_, err := cli.Projects.Locations.Services.SetIamPolicy(fmt.Sprintf("projects/%s/locations/%s/services/%s", project, region, name),
		&run.SetIamPolicyRequest{
			Policy: policy,
		},
	).Do()

	return err
}

func waitForRunServiceReady(ctx context.Context, cli *run.APIService, project, name string) (svc *run.Service, ready bool, msg string, err error) {
	t := time.NewTicker(time.Second * 5)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, false, "", ctx.Err()
		case <-t.C:
			svc, err := getRunService(cli, project, name)
			if err != nil {
				return nil, false, "", fmt.Errorf("failed to query run service for readiness: %w", err)
			}

			if svc.Metadata == nil || svc.Status.ObservedGeneration != svc.Metadata.Generation {
				continue
			}

			for _, c := range svc.Status.Conditions {
				if c.Type == CloudRunReady {
					switch c.Status {
					case CloudRunStatusTrue:
						return svc, true, "", nil
					case CloudRunStatusFalse:
						return svc, false, c.Message, nil
					}
				}
			}
		}
	}
}

func waitForRunServiceDeleted(ctx context.Context, cli *run.APIService, project, name string) error {
	t := time.NewTicker(time.Second * 5)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			_, err := getRunService(cli, project, name)
			if ErrIs404(err) {
				return nil
			}

			if err != nil {
				return fmt.Errorf("failed to query run service for readiness: %w", err)
			}
		}
	}
}
