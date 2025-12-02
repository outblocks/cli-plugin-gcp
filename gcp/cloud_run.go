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
	StartupCPUBoost      fields.BoolInputField   `default:"false"`

	LivenessProbeHTTPPath            fields.StringInputField
	LivenessProbeGRPCService         fields.StringInputField
	LivenessProbePort                fields.IntInputField
	LivenessProbeInitialDelaySeconds fields.IntInputField `default:"0"`
	LivenessProbePeriodSeconds       fields.IntInputField `default:"10"`
	LivenessProbeTimeoutSeconds      fields.IntInputField `default:"1"`
	LivenessProbeFailureThreshold    fields.IntInputField `default:"3"`

	StartupProbeHTTPPath            fields.StringInputField
	StartupProbeGRPCService         fields.StringInputField
	StartupProbePort                fields.IntInputField
	StartupProbeInitialDelaySeconds fields.IntInputField `default:"0"`
	StartupProbePeriodSeconds       fields.IntInputField `default:"10"`
	StartupProbeTimeoutSeconds      fields.IntInputField `default:"1"`
	StartupProbeFailureThreshold    fields.IntInputField `default:"3"`
}

func (o *CloudRun) ReferenceID() string {
	return fields.GenerateID("locations/%s/namespaces/%s/services/%s", o.Region, o.ProjectID, o.Name)
}

func (o *CloudRun) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *CloudRun) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

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
		o.StartupCPUBoost.UnsetCurrent()
		o.ExecutionEnvironment.UnsetCurrent()

		o.LivenessProbeHTTPPath.UnsetCurrent()
		o.LivenessProbeGRPCService.UnsetCurrent()
		o.LivenessProbePort.UnsetCurrent()
		o.LivenessProbeInitialDelaySeconds.UnsetCurrent()
		o.LivenessProbePeriodSeconds.UnsetCurrent()
		o.LivenessProbeTimeoutSeconds.UnsetCurrent()
		o.LivenessProbeFailureThreshold.UnsetCurrent()

		o.StartupProbeHTTPPath.UnsetCurrent()
		o.StartupProbeGRPCService.UnsetCurrent()
		o.StartupProbePort.UnsetCurrent()
		o.StartupProbeInitialDelaySeconds.UnsetCurrent()
		o.StartupProbePeriodSeconds.UnsetCurrent()
		o.StartupProbeTimeoutSeconds.UnsetCurrent()
		o.StartupProbeFailureThreshold.UnsetCurrent()

		return nil
	}

	for _, cond := range svc.Status.Conditions {
		if cond.Type != CloudRunReady {
			continue
		}

		o.Ready.SetCurrent(cond.Status == CloudRunStatusTrue)
		o.StatusMessage.SetCurrent(cond.Message)
	}

	args := make([]any, len(svc.Spec.Template.Spec.Containers[0].Args))
	for i, v := range svc.Spec.Template.Spec.Containers[0].Args {
		args[i] = v
	}

	command := make([]any, len(svc.Spec.Template.Spec.Containers[0].Command))
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
	o.StartupCPUBoost.SetCurrent(svc.Spec.Template.Metadata.Annotations["run.googleapis.com/startup-cpu-boost"] == "true")
	o.ExecutionEnvironment.SetCurrent(svc.Spec.Template.Metadata.Annotations["run.googleapis.com/execution-environment"])

	v, _ := strconv.Atoi(svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/minScale"])
	o.MinScale.SetCurrent(v)

	v, _ = strconv.Atoi(svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/maxScale"])
	o.MaxScale.SetCurrent(v)

	envVars := make(map[string]any)

	for _, e := range svc.Spec.Template.Spec.Containers[0].Env {
		envVars[e.Name] = e.Value
	}

	o.EnvVars.SetCurrent(envVars)

	if svc.Spec.Template.Spec.Containers[0].LivenessProbe != nil {
		if svc.Spec.Template.Spec.Containers[0].LivenessProbe.HttpGet != nil {
			o.LivenessProbeHTTPPath.SetCurrent(svc.Spec.Template.Spec.Containers[0].LivenessProbe.HttpGet.Path)
			o.LivenessProbePort.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].LivenessProbe.HttpGet.Port))
		}

		if svc.Spec.Template.Spec.Containers[0].LivenessProbe.Grpc != nil {
			o.LivenessProbeGRPCService.SetCurrent(svc.Spec.Template.Spec.Containers[0].LivenessProbe.Grpc.Service)
			o.LivenessProbePort.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].LivenessProbe.Grpc.Port))
		}

		o.LivenessProbeInitialDelaySeconds.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].LivenessProbe.InitialDelaySeconds))
		o.LivenessProbePeriodSeconds.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].LivenessProbe.PeriodSeconds))
		o.LivenessProbeTimeoutSeconds.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].LivenessProbe.TimeoutSeconds))
		o.LivenessProbeFailureThreshold.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].LivenessProbe.FailureThreshold))
	} else {
		o.LivenessProbeHTTPPath.UnsetCurrent()
		o.LivenessProbeGRPCService.UnsetCurrent()
		o.LivenessProbeInitialDelaySeconds.UnsetCurrent()
		o.LivenessProbePeriodSeconds.UnsetCurrent()
		o.LivenessProbeTimeoutSeconds.UnsetCurrent()
		o.LivenessProbeFailureThreshold.UnsetCurrent()
	}

	if svc.Spec.Template.Spec.Containers[0].StartupProbe != nil {
		if svc.Spec.Template.Spec.Containers[0].StartupProbe.HttpGet != nil {
			o.StartupProbeHTTPPath.SetCurrent(svc.Spec.Template.Spec.Containers[0].StartupProbe.HttpGet.Path)
			o.StartupProbePort.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].StartupProbe.HttpGet.Port))
		}

		if svc.Spec.Template.Spec.Containers[0].StartupProbe.Grpc != nil {
			o.StartupProbeGRPCService.SetCurrent(svc.Spec.Template.Spec.Containers[0].StartupProbe.Grpc.Service)
			o.StartupProbePort.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].StartupProbe.Grpc.Port))
		}

		o.StartupProbeInitialDelaySeconds.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].StartupProbe.InitialDelaySeconds))
		o.StartupProbePeriodSeconds.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].StartupProbe.PeriodSeconds))
		o.StartupProbeTimeoutSeconds.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].StartupProbe.TimeoutSeconds))
		o.StartupProbeFailureThreshold.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].StartupProbe.FailureThreshold))
	} else {
		o.StartupProbeHTTPPath.UnsetCurrent()
		o.StartupProbeGRPCService.UnsetCurrent()
		o.StartupProbeInitialDelaySeconds.UnsetCurrent()
		o.StartupProbePeriodSeconds.UnsetCurrent()
		o.StartupProbeTimeoutSeconds.UnsetCurrent()
		o.StartupProbeFailureThreshold.UnsetCurrent()
	}

	// Check IAM policy.

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

func (o *CloudRun) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

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

func (o *CloudRun) Update(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

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

func (o *CloudRun) Delete(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

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
		envVars = append(envVars, &run.EnvVar{Name: k, Value: v.(string)}) //nolint:errcheck
	}

	command := o.Command.Wanted()
	commandStr := make([]string, len(command))

	for i, v := range command {
		commandStr[i] = v.(string) //nolint:errcheck
	}

	args := o.Args.Wanted()
	argsStr := make([]string, len(args))

	for i, v := range args {
		argsStr[i] = v.(string) //nolint:errcheck
	}

	cpuThrottling := "false"
	if o.CPUThrottling.Wanted() {
		cpuThrottling = "true"
	}

	startupCpuBoost := "false"
	if o.StartupCPUBoost.Wanted() {
		startupCpuBoost = "true"
	}

	var startupProbe, livenessProbe *run.Probe

	if o.StartupProbeHTTPPath.Wanted() != "" || o.StartupProbeGRPCService.Wanted() != "" {
		startupProbe = &run.Probe{}

		port := o.Port.Wanted()
		if o.StartupProbePort.Wanted() != 0 {
			port = o.StartupProbePort.Wanted()
		}

		if o.StartupProbeHTTPPath.Wanted() != "" {
			startupProbe.HttpGet = &run.HTTPGetAction{
				Port: int64(port),
				Path: o.StartupProbeHTTPPath.Wanted(),
			}
		}

		if o.StartupProbeGRPCService.Wanted() != "" {
			startupProbe.Grpc = &run.GRPCAction{
				Port:    int64(port),
				Service: o.StartupProbeGRPCService.Wanted(),
			}
		}

		startupProbe.InitialDelaySeconds = int64(o.StartupProbeInitialDelaySeconds.Wanted())
		startupProbe.PeriodSeconds = int64(o.StartupProbePeriodSeconds.Wanted())
		startupProbe.TimeoutSeconds = int64(o.StartupProbeTimeoutSeconds.Wanted())
		startupProbe.FailureThreshold = int64(o.StartupProbeFailureThreshold.Wanted())
	}

	if o.LivenessProbeHTTPPath.Wanted() != "" || o.LivenessProbeGRPCService.Wanted() != "" {
		livenessProbe = &run.Probe{}

		port := o.Port.Wanted()
		if o.LivenessProbePort.Wanted() != 0 {
			port = o.LivenessProbePort.Wanted()
		}

		if o.LivenessProbeHTTPPath.Wanted() != "" {
			livenessProbe.HttpGet = &run.HTTPGetAction{
				Path: o.LivenessProbeHTTPPath.Wanted(),
				Port: int64(port),
			}
		}

		if o.LivenessProbeGRPCService.Wanted() != "" {
			livenessProbe.Grpc = &run.GRPCAction{
				Service: o.LivenessProbeGRPCService.Wanted(),
				Port:    int64(port),
			}
		}

		livenessProbe.InitialDelaySeconds = int64(o.LivenessProbeInitialDelaySeconds.Wanted())
		livenessProbe.PeriodSeconds = int64(o.LivenessProbePeriodSeconds.Wanted())
		livenessProbe.TimeoutSeconds = int64(o.LivenessProbeTimeoutSeconds.Wanted())
		livenessProbe.FailureThreshold = int64(o.LivenessProbeFailureThreshold.Wanted())
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
						"run.googleapis.com/startup-cpu-boost":     startupCpuBoost,
					},
				},
				Spec: &run.RevisionSpec{
					ContainerConcurrency: int64(o.ContainerConcurrency.Wanted()),
					TimeoutSeconds:       int64(o.TimeoutSeconds.Wanted()),
					Containers: []*run.Container{
						{
							StartupProbe:  startupProbe,
							LivenessProbe: livenessProbe,
							Command:       commandStr,
							Args:          argsStr,
							Image:         o.Image.Wanted(),
							Env:           envVars,
							Ports:         []*run.ContainerPort{{ContainerPort: int64(o.Port.Wanted())}},
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
