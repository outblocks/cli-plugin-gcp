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
	Image     fields.StringInputField
	IsPublic  fields.BoolInputField

	MinScale             fields.IntInputField    `default:"0"`
	MaxScale             fields.IntInputField    `default:"100"`
	CPULimit             fields.StringInputField `default:"1000m"`
	MemoryLimit          fields.StringInputField `default:"128Mi"`
	ContainerConcurrency fields.IntInputField    `default:"250"`
	TimeoutSeconds       fields.IntInputField    `default:"300"`
	Port                 fields.IntInputField    `default:"80"`
	EnvVars              fields.MapInputField
}

func (o *CloudRun) GetName() string {
	return o.Name.Any()
}

func (o *CloudRun) Read(ctx context.Context, meta interface{}) error {
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
		o.SetNew(true)

		return nil
	}

	if err != nil {
		return fmt.Errorf("error fetching cloud run service status: %w", err)
	}

	isNew := o.IsNew()

	o.SetNew(false)
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)
	o.Region.SetCurrent(region)

	if svc.Spec == nil || svc.Spec.Template == nil || svc.Spec.Template.Metadata == nil || len(svc.Spec.Template.Spec.Containers) != 1 || svc.Spec.Template.Spec.Containers[0].Resources == nil || len(svc.Spec.Template.Spec.Containers[0].Ports) != 1 {
		o.MinScale.UnsetCurrent()
		o.MaxScale.UnsetCurrent()
		o.CPULimit.UnsetCurrent()
		o.MemoryLimit.UnsetCurrent()
		o.ContainerConcurrency.UnsetCurrent()
		o.TimeoutSeconds.UnsetCurrent()
		o.Port.UnsetCurrent()
		o.EnvVars.UnsetCurrent()

		return nil
	}

	v, _ := strconv.Atoi(svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/minScale"])
	o.MinScale.SetCurrent(v)

	v, _ = strconv.Atoi(svc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/maxScale"])
	o.MaxScale.SetCurrent(v)

	o.CPULimit.SetCurrent(svc.Spec.Template.Spec.Containers[0].Resources.Limits["cpu"])
	o.MemoryLimit.SetCurrent(svc.Spec.Template.Spec.Containers[0].Resources.Limits["memory"])
	o.ContainerConcurrency.SetCurrent(int(svc.Spec.Template.Spec.ContainerConcurrency))
	o.TimeoutSeconds.SetCurrent(int(svc.Spec.Template.Spec.TimeoutSeconds))
	o.Port.SetCurrent(int(svc.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort))

	envVars := make(map[string]interface{})

	for _, e := range svc.Spec.Template.Spec.Containers[0].Env {
		envVars[e.Name] = e.Value
	}

	o.EnvVars.SetCurrent(envVars)

	if isNew {
		o.IsPublic.UnsetCurrent()
	}

	return nil
}

func (o *CloudRun) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPRunClient(ctx, o.Region.Wanted())
	if err != nil {
		return err
	}

	_, err = createRunService(cli, o.ProjectID.Wanted(), o.makeRunService())
	if err != nil {
		return err
	}

	err = waitForRunServiceReady(ctx, cli, o.ProjectID.Wanted(), o.Name.Wanted())
	if err != nil {
		return err
	}

	if o.IsPublic.Wanted() {
		return setRunServiceIAMPolicy(cli, o.ProjectID.Wanted(), o.Name.Wanted(), o.Region.Wanted(), o.IsPublic.Wanted())
	}

	return nil
}

func (o *CloudRun) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPRunClient(ctx, o.Region.Wanted())
	if err != nil {
		return err
	}

	_, err = updateRunService(cli, o.ProjectID.Wanted(), o.Name.Wanted(), o.makeRunService())
	if err != nil {
		return err
	}

	err = waitForRunServiceReady(ctx, cli, o.ProjectID.Wanted(), o.Name.Wanted())
	if err != nil {
		return err
	}

	if o.IsPublic.IsChanged() {
		return setRunServiceIAMPolicy(cli, o.ProjectID.Wanted(), o.Name.Wanted(), o.Region.Wanted(), o.IsPublic.Wanted())
	}

	return nil
}

func (o *CloudRun) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPRunClient(ctx, o.Region.Current())
	if err != nil {
		return err
	}

	_, err = deleteRunService(cli, o.ProjectID.Current(), o.Name.Current())

	return err
}

func (o *CloudRun) makeRunService() *run.Service {
	var envVars []*run.EnvVar
	for k, v := range o.EnvVars.Wanted() {
		envVars = append(envVars, &run.EnvVar{Name: k, Value: v.(string)})
	}

	svc := &run.Service{
		ApiVersion: "serving.knative.dev/v1",
		Kind:       "Service",
		Metadata: &run.ObjectMeta{
			Name: o.Name.Wanted(),
			Annotations: map[string]string{
				"run.googleapis.com/ingress": "internal-and-cloud-load-balancing",
			},
		},
		Spec: &run.ServiceSpec{
			Template: &run.RevisionTemplate{
				Metadata: &run.ObjectMeta{
					Annotations: map[string]string{
						"run.googleapis.com/client-name":   "outblocks",
						"autoscaling.knative.dev/minScale": strconv.Itoa(o.MinScale.Wanted()),
						"autoscaling.knative.dev/maxScale": strconv.Itoa(o.MaxScale.Wanted()),
					},
				},
				Spec: &run.RevisionSpec{
					ContainerConcurrency: int64(o.ContainerConcurrency.Wanted()),
					TimeoutSeconds:       int64(o.TimeoutSeconds.Wanted()),
					Containers: []*run.Container{
						{
							Image: o.Image.Wanted(),
							Env:   envVars,
							Ports: []*run.ContainerPort{{ContainerPort: int64(o.Port.Wanted())}},
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

func setRunServiceIAMPolicy(cli *run.APIService, project, name, region string, public bool) error {
	var policy *run.Policy

	if public {
		policy = &run.Policy{Bindings: []*run.Binding{{
			Members: []string{"allUsers"},
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

func waitForRunServiceReady(ctx context.Context, cli *run.APIService, project, name string) error {
	t := time.NewTicker(time.Second * 5)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			svc, err := getRunService(cli, project, name)
			if err != nil {
				return fmt.Errorf("failed to query service for readiness: %w", err)
			}

			for _, c := range svc.Status.Conditions {
				if c.Type == "Ready" {
					if c.Status == "True" {
						return nil
					} else if c.Status == "False" {
						return fmt.Errorf("service could not become ready (status:%s) (reason:%s) %s",
							c.Status, c.Reason, c.Message)
					}
				}
			}
		}
	}
}
