package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/creasty/defaults"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/compute/v1"
)

const BackendServiceName = "backend service"

type BackendService struct {
	Name      string                 `json:"name"`
	ProjectID string                 `json:"project_id" mapstructure:"project_id"`
	NEG       string                 `json:"serverless_neg" mapstructure:"serverless_neg"`
	Options   *BackendServiceOptions `json:"options"`
}

func (o *BackendService) Key() string {
	return o.NEG
}

func (o *BackendService) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		BackendService
		Type string `json:"type"`
	}{
		BackendService: *o,
		Type:           "gcp_backend_service",
	})
}

type BackendServiceCreate BackendService

func (o *BackendServiceCreate) Key() string {
	return o.NEG
}

func (o *BackendServiceCreate) ID() string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/backendServices/%s", o.ProjectID, o.Name)
}

type BackendServicePlan BackendService

func (o *BackendServicePlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

type BackendServiceOptionsCDN struct {
	Enabled        bool
	CacheMode      string `default:"CACHE_ALL_STATIC"`
	CacheKeyPolicy struct {
		IncludeHost        bool `default:"1"`
		IncludeProtocol    bool `default:"1"`
		IncludeQueryString bool `default:"1"`
	}
	DefaultTTL int `default:"3600"`
	MaxTTL     int `default:"86400"`
	ClientTTL  int `default:"3600"`
}

type BackendServiceOptions struct {
	CDN BackendServiceOptionsCDN
}

func (o *BackendServiceOptions) ConflictsWith(c *BackendServiceOptions) bool {
	if o == nil {
		o = &BackendServiceOptions{}
	}

	if c == nil {
		c = &BackendServiceOptions{}
	}

	err := defaults.Set(o)
	if err != nil {
		panic(err)
	}

	err = defaults.Set(c)
	if err != nil {
		panic(err)
	}

	return o.CDN.Enabled != c.CDN.Enabled
}

func (o *BackendServiceOptions) Equals(c *BackendServiceOptions) bool {
	if o == nil {
		o = &BackendServiceOptions{}
	}

	if c == nil {
		c = &BackendServiceOptions{}
	}

	err := defaults.Set(o)
	if err != nil {
		panic(err)
	}

	err = defaults.Set(c)
	if err != nil {
		panic(err)
	}

	return reflect.DeepEqual(o, c)
}

func makeBackendService(name, neg string, opts *BackendServiceOptions) *compute.BackendService {
	if opts == nil {
		opts = &BackendServiceOptions{}
	}

	err := defaults.Set(opts)
	if err != nil {
		panic(err)
	}

	return &compute.BackendService{
		Name:      name,
		EnableCDN: opts.CDN.Enabled,
		CdnPolicy: &compute.BackendServiceCdnPolicy{
			CacheMode: opts.CDN.CacheMode,
			CacheKeyPolicy: &compute.CacheKeyPolicy{
				IncludeHost:        opts.CDN.CacheKeyPolicy.IncludeHost,
				IncludeProtocol:    opts.CDN.CacheKeyPolicy.IncludeProtocol,
				IncludeQueryString: opts.CDN.CacheKeyPolicy.IncludeQueryString,
			},
			DefaultTtl: int64(opts.CDN.DefaultTTL),
			MaxTtl:     int64(opts.CDN.MaxTTL),
			ClientTtl:  int64(opts.CDN.ClientTTL),
		},
		Backends: []*compute.Backend{
			{
				Group: neg,
			},
		},
	}
}

func (o *BackendService) verify(cli *compute.Service, c *BackendServiceCreate) error {
	name := o.Name
	projectID := o.ProjectID

	if name == "" && c != nil {
		name = c.Name
		projectID = c.ProjectID
	}

	if name == "" {
		return nil
	}

	obj, err := cli.BackendServices.Get(projectID, name).Do()
	if ErrIs404(err) {
		*o = BackendService{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = projectID

	if len(obj.Backends) == 1 {
		o.NEG = obj.Backends[0].Group
	}

	return nil
}

func (o *BackendService) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *BackendServiceCreate
	)

	if dest != nil {
		c = dest.(*BackendServiceCreate)
	}

	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return nil, err
	}

	// Fetch current state if needed.
	if verify {
		err := o.verify(cli, c)
		if err != nil {
			return nil, err
		}
	}

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(BackendServiceName, o.Name),
				append(ops, deleteBackendServiceOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(BackendServiceName, c.Name),
			append(ops, createBackendServiceOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID || o.Options.ConflictsWith(c.Options) {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(BackendServiceName, c.Name, "forces recreate"),
			append(ops, deleteBackendServiceOp(o), createBackendServiceOp(c))), nil
	}

	// Check for partial updates.
	if o.NEG != c.NEG || !o.Options.Equals(c.Options) {
		plan := &BackendServicePlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
			NEG:       c.NEG,
			Options:   c.Options,
		}

		return types.NewPlanActionUpdate(key, plugin_util.UpdateDesc(BackendServiceName, c.Name, "in-place"),
			append(ops, &types.PlanActionOperation{Operation: types.PlanOpUpdate, Steps: 1, Data: plan.Encode()})), nil
	}

	return nil, nil
}

func deleteBackendServiceOp(o *BackendService) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&BackendServicePlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createBackendServiceOp(c *BackendServiceCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&BackendServicePlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
			NEG:       c.NEG,
			Options:   c.Options,
		}).Encode(),
	}
}

func decodeBackendServicePlan(p *types.PlanActionOperation) (ret *BackendServicePlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func (o *BackendService) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeBackendServicePlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			oper, err := cli.BackendServices.Delete(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(BackendServiceName, plan.Name))

		case types.PlanOpAdd:
			// Creation.
			oper, err := cli.BackendServices.Insert(plan.ProjectID, makeBackendService(plan.Name, plan.NEG, plan.Options)).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(BackendServiceName, plan.Name))

			_, err = cli.BackendServices.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.Name = plan.Name
			o.ProjectID = plan.ProjectID
			o.NEG = plan.NEG
			o.Options = plan.Options

		case types.PlanOpUpdate:
			oper, err := cli.BackendServices.Patch(plan.ProjectID, plan.Name, makeBackendService(plan.Name, plan.NEG, plan.Options)).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(BackendServiceName, plan.Name))

			_, err = cli.BackendServices.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.NEG = plan.NEG
			o.Options = plan.Options
		}
	}

	return nil
}
