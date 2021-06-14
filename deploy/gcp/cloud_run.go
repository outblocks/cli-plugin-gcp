package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/creasty/defaults"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/run/v1"
)

const CloudRunName = "cloud run"

type CloudRun struct {
	Name      string             `json:"name"`
	ProjectID string             `json:"project_id" mapstructure:"project_id"`
	Region    string             `json:"region"`
	Image     string             `json:"image"`
	IsPublic  *bool              `json:"is_public" mapstructure:"is_public"`
	Options   *RunServiceOptions `json:"options"`
}

func (o *CloudRun) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		CloudRun
		Type string `json:"type"`
	}{
		CloudRun: *o,
		Type:     "gcp_cloud_run",
	})
}

type CloudRunCreate struct {
	Name      string
	Image     string
	ProjectID string
	Region    string
	IsPublic  bool
	Options   *RunServiceOptions
}

type CloudRunPlan CloudRun

func (o *CloudRunPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func decodeCloudRunPlan(p *types.PlanActionOperation) (ret *CloudRunPlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

type RunServiceOptions struct {
	MinScale             int
	MaxScale             int    `default:"100"`
	CPULimit             string `default:"1000m"`
	MemoryLimit          string `default:"128Mi"`
	ContainerConcurrency int    `default:"250"`
	TimeoutSeconds       int    `default:"300"`
	Port                 int    `default:"80"`
	EnvVars              map[string]string
}

func makeRunService(name, image string, opts *RunServiceOptions) *run.Service {
	if opts == nil {
		opts = &RunServiceOptions{}
	}

	err := defaults.Set(opts)
	if err != nil {
		panic(err)
	}

	var envVars []*run.EnvVar
	for k, v := range opts.EnvVars {
		envVars = append(envVars, &run.EnvVar{Name: k, Value: v})
	}

	svc := &run.Service{
		ApiVersion: "serving.knative.dev/v1",
		Kind:       "Service",
		Metadata: &run.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				"run.googleapis.com/ingress": "internal-and-cloud-load-balancing",
			},
		},
		Spec: &run.ServiceSpec{
			Template: &run.RevisionTemplate{
				Metadata: &run.ObjectMeta{
					Annotations: map[string]string{
						"run.googleapis.com/client-name":   "outblocks",
						"autoscaling.knative.dev/minScale": strconv.Itoa(opts.MinScale),
						"autoscaling.knative.dev/maxScale": strconv.Itoa(opts.MaxScale),
					},
				},
				Spec: &run.RevisionSpec{
					ContainerConcurrency: int64(opts.ContainerConcurrency),
					TimeoutSeconds:       int64(opts.TimeoutSeconds),
					Containers: []*run.Container{
						{
							Image: image,
							Env:   envVars,
							Ports: []*run.ContainerPort{{ContainerPort: int64(opts.Port)}},
							Resources: &run.ResourceRequirements{
								Limits: map[string]string{
									"cpu":    opts.CPULimit,
									"memory": opts.MemoryLimit,
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

func createRunService(cli *run.APIService, project, name, image string, opts *RunServiceOptions) (*run.Service, error) {
	return cli.Namespaces.Services.Create(fmt.Sprintf("namespaces/%s", project), makeRunService(name, image, opts)).Do()
}

func getRunService(cli *run.APIService, project, name string) (*run.Service, error) {
	return cli.Namespaces.Services.Get(fmt.Sprintf("namespaces/%s/services/%s", project, name)).Do()
}

func deleteRunService(cli *run.APIService, project, name string) (*run.Status, error) {
	return cli.Namespaces.Services.Delete(fmt.Sprintf("namespaces/%s/services/%s", project, name)).Do()
}

func updateRunService(cli *run.APIService, project, name, image string, opts *RunServiceOptions) (*run.Service, error) {
	return cli.Namespaces.Services.ReplaceService(fmt.Sprintf("namespaces/%s/services/%s", project, name), makeRunService(name, image, opts)).Do()
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

func runServiceRequiresUpdate(s1, s2 *run.Service) bool {
	if s2.Metadata == nil || s2.Metadata.Annotations == nil || s2.Spec == nil || s2.Spec.Template == nil || s2.Spec.Template.Metadata == nil || len(s2.Spec.Template.Spec.Containers) != len(s1.Spec.Template.Spec.Containers) {
		return true
	}

	keys := []string{"run.googleapis.com/ingress"}

	if util.PartialMapCompare(s1.Metadata.Annotations, s2.Metadata.Annotations, keys) {
		return true
	}

	keys = []string{"autoscaling.knative.dev/minScale", "autoscaling.knative.dev/maxScale"}

	if util.PartialMapCompare(s1.Spec.Template.Metadata.Annotations, s2.Spec.Template.Metadata.Annotations, keys) {
		return true
	}

	cont1 := s1.Spec.Template.Spec.Containers[0]
	cont2 := s2.Spec.Template.Spec.Containers[0]

	if cont1.Image != cont2.Image || !reflect.DeepEqual(cont1.Command, cont2.Command) || !reflect.DeepEqual(cont1.Args, cont2.Args) || !reflect.DeepEqual(cont1.Ports, cont2.Ports) || !reflect.DeepEqual(cont1.Resources, cont2.Resources) {
		return true
	}

	if len(cont1.Env) != len(cont2.Env) {
		return true
	}

	env1 := make(map[string]string)
	env2 := make(map[string]string)

	for _, v := range cont1.Env {
		env1[v.Name] = v.Value
	}

	for _, v := range cont2.Env {
		env2[v.Name] = v.Value
	}

	return !reflect.DeepEqual(env1, env2)
}

func (o *CloudRun) verify(pctx *config.PluginContext, c *CloudRunCreate) error {
	name := o.Name
	region := o.Region
	project := o.ProjectID

	if name == "" && c != nil {
		name = c.Name
		region = c.Region
		project = c.ProjectID
	}

	if name == "" {
		return nil
	}

	cli, err := pctx.GCPRunClient(region)
	if err != nil {
		return err
	}

	_, err = getRunService(cli, project, name)
	if ErrIs404(err) {
		*o = CloudRun{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = project
	o.Region = region

	return nil
}

func deleteCloudRunOp(o *CloudRun) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&CloudRunPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
			Region:    o.Region,
		}).Encode(),
	}
}

func createCloudRunOp(c *CloudRunCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     3,
		Operation: types.PlanOpAdd,
		Data: (&CloudRunPlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
			Region:    c.Region,
			Image:     c.Image,
			IsPublic:  &c.IsPublic,
			Options:   c.Options,
		}).Encode(),
	}
}

func (o *CloudRun) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *CloudRunCreate
	)

	if dest != nil {
		c = dest.(*CloudRunCreate)
	}

	pctx := ctx.(*config.PluginContext)

	// Fetch current state if needed.
	if verify {
		err := o.verify(pctx, c)
		if err != nil {
			return nil, err
		}
	}

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(CloudRunName, o.Name),
				append(ops, deleteCloudRunOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(CloudRunName, c.Name),
			append(ops, createCloudRunOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID || o.Region != c.Region {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(CloudRunName, c.Name, "forces recreate"),
			append(ops, deleteCloudRunOp(o), createCloudRunOp(c))), nil
	}

	// Check for partial updates.
	steps := 0

	plan := &CloudRunPlan{
		Name:      c.Name,
		ProjectID: c.ProjectID,
		Region:    c.Region,
		Image:     c.Image,
	}

	if runServiceRequiresUpdate(makeRunService(o.Name, o.Image, o.Options), makeRunService(c.Name, c.Image, c.Options)) {
		steps += 2
		plan.Options = c.Options
	}

	if !util.CompareBoolPtr(o.IsPublic, &c.IsPublic) {
		steps += 1
		plan.IsPublic = &c.IsPublic
	}

	if steps > 0 {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(CloudRunName, c.Name, "in-place"),
			append(ops, &types.PlanActionOperation{
				Steps:     steps,
				Operation: types.PlanOpUpdate,
				Data:      plan.Encode()})), nil
	}

	return nil, nil
}

func (o *CloudRun) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	// Process operations.
	for _, op := range ops {
		plan, err := decodeCloudRunPlan(op)
		if err != nil {
			return err
		}

		cli, err := pctx.GCPRunClient(plan.Region)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			_, err = deleteRunService(cli, plan.ProjectID, plan.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(CloudRunName, plan.Name))

		case types.PlanOpUpdate:
			// Update.
			if plan.Options != nil {
				_, err = updateRunService(cli, plan.ProjectID, plan.Name, plan.Image, plan.Options)
				if err != nil {
					return err
				}

				o.Image = plan.Image
				o.Options = plan.Options

				callback(plugin_util.UpdateDesc(CloudRunName, o.Name))

				err = waitForRunServiceReady(pctx, cli, plan.ProjectID, plan.Name)
				if err != nil {
					return err
				}

				callback(plugin_util.UpdateDesc(CloudRunName, o.Name, "ready"))
			}

			if plan.IsPublic != nil {
				err = setRunServiceIAMPolicy(cli, plan.ProjectID, plan.Name, plan.Region, *plan.IsPublic)
				if err != nil {
					return err
				}

				callback(plugin_util.UpdateDesc(CloudRunName, o.Name, "in-place"))

				o.IsPublic = plan.IsPublic
			}

		case types.PlanOpAdd:
			// Creation.
			_, err = createRunService(cli, plan.ProjectID, plan.Name, plan.Image, plan.Options)
			if err != nil {
				return err
			}

			o.Name = plan.Name
			o.ProjectID = plan.ProjectID
			o.Region = plan.Region
			o.Image = plan.Image
			o.Options = plan.Options

			callback(plugin_util.AddDesc(CloudRunName, o.Name))

			err = waitForRunServiceReady(pctx, cli, plan.ProjectID, plan.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(CloudRunName, o.Name, "ready"))

			if plan.IsPublic != nil {
				err = setRunServiceIAMPolicy(cli, plan.ProjectID, plan.Name, plan.Region, *plan.IsPublic)
				if err != nil {
					return err
				}

				callback(plugin_util.UpdateDesc(CloudRunName, o.Name, "in-place"))

				o.IsPublic = plan.IsPublic
			}
		}
	}

	return nil
}
