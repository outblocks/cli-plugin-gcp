package gcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/compute/v1"
)

const TargetHTTPProxyName = "target HTTP proxy"

type TargetHTTPProxy struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`
	URLMap    string `json:"url_map" mapstructure:"url_map"`
}

func (o *TargetHTTPProxy) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TargetHTTPProxy
		Type string `json:"type"`
	}{
		TargetHTTPProxy: *o,
		Type:            "gcp_target_http_proxy",
	})
}

func (o *TargetHTTPProxy) ID() string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/targetHttpProxies/%s", o.ProjectID, o.Name)
}

type TargetHTTPProxyCreate TargetHTTPProxy

func (o *TargetHTTPProxyCreate) ID() string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/targetHttpProxies/%s", o.ProjectID, o.Name)
}

type TargetHTTPProxyPlan TargetHTTPProxy

func (o *TargetHTTPProxyPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func (o *TargetHTTPProxy) verify(cli *compute.Service, c *TargetHTTPProxyCreate) error {
	name := o.Name
	projectID := o.ProjectID

	if name == "" && c != nil {
		name = c.Name
		projectID = c.ProjectID
	}

	if name == "" {
		return nil
	}

	obj, err := cli.TargetHttpProxies.Get(projectID, name).Do()
	if ErrIs404(err) {
		*o = TargetHTTPProxy{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = projectID
	o.URLMap = obj.UrlMap

	return nil
}

func (o *TargetHTTPProxy) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *TargetHTTPProxyCreate
	)

	if dest != nil {
		c = dest.(*TargetHTTPProxyCreate)
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
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(TargetHTTPProxyName, o.Name),
				append(ops, deleteTargetHTTPProxyOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(TargetHTTPProxyName, c.Name),
			append(ops, createTargetHTTPProxyOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(TargetHTTPProxyName, c.Name, "forces recreate"),
			append(ops, deleteTargetHTTPProxyOp(o), createTargetHTTPProxyOp(c))), nil
	}

	// Check for partial updates.
	if o.URLMap != c.URLMap {
		plan := &TargetHTTPProxyPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
			URLMap:    o.URLMap,
		}

		return types.NewPlanActionUpdate(key, plugin_util.UpdateDesc(TargetHTTPProxyName, c.Name, "in-place"),
			append(ops, &types.PlanActionOperation{Operation: types.PlanOpUpdate, Steps: 1, Data: plan.Encode()})), nil
	}

	return nil, nil
}

func deleteTargetHTTPProxyOp(o *TargetHTTPProxy) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&TargetHTTPProxyPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createTargetHTTPProxyOp(c *TargetHTTPProxyCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&TargetHTTPProxyPlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
			URLMap:    c.URLMap,
		}).Encode(),
	}
}

func decodeTargetHTTPProxyPlan(p *types.PlanActionOperation) (ret *TargetHTTPProxyPlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func (o *TargetHTTPProxy) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeTargetHTTPProxyPlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			oper, err := cli.TargetHttpProxies.Delete(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(TargetHTTPProxyName, plan.Name))

		case types.PlanOpAdd:
			// Creation.
			oper, err := cli.TargetHttpProxies.Insert(plan.ProjectID, &compute.TargetHttpProxy{
				Name:   plan.Name,
				UrlMap: plan.URLMap,
			}).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(TargetHTTPProxyName, plan.Name))

			_, err = cli.TargetHttpProxies.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.Name = plan.Name
			o.ProjectID = plan.ProjectID
			o.URLMap = plan.URLMap

		case types.PlanOpUpdate:
			oper, err := cli.TargetHttpProxies.Patch(plan.ProjectID, plan.Name, &compute.TargetHttpProxy{
				Name:   plan.Name,
				UrlMap: plan.URLMap,
			}).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(TargetHTTPProxyName, plan.Name))

			_, err = cli.TargetHttpProxies.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.URLMap = plan.URLMap
		}
	}

	return nil
}
