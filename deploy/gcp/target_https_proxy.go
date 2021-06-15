package gcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/compute/v1"
)

const TargetHTTPSProxyName = "target HTTPS proxy"

type TargetHTTPSProxy struct {
	Name            string   `json:"name"`
	ProjectID       string   `json:"project_id" mapstructure:"project_id"`
	URLMap          string   `json:"url_map" mapstructure:"url_map"`
	SSLCertificates []string `json:"ssl_certificates" mapstructure:"ssl_certificates"`
}

func (o *TargetHTTPSProxy) Key() string {
	return strings.Join(o.SSLCertificates, ",")
}

func (o *TargetHTTPSProxy) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TargetHTTPSProxy
		Type string `json:"type"`
	}{
		TargetHTTPSProxy: *o,
		Type:             "gcp_target_https_proxy",
	})
}

type TargetHTTPSProxyCreate TargetHTTPSProxy

func (o *TargetHTTPSProxyCreate) Key() string {
	return strings.Join(o.SSLCertificates, ",")
}

type TargetHTTPSProxyPlan TargetHTTPSProxy

func (o *TargetHTTPSProxyPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func (o *TargetHTTPSProxy) verify(cli *compute.Service, c *TargetHTTPSProxyCreate) error {
	name := o.Name
	projectID := o.ProjectID

	if name == "" && c != nil {
		name = c.Name
		projectID = c.ProjectID
	}

	if name == "" {
		return nil
	}

	obj, err := cli.TargetHttpsProxies.Get(projectID, name).Do()
	if ErrIs404(err) {
		*o = TargetHTTPSProxy{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = projectID
	o.URLMap = obj.UrlMap
	o.SSLCertificates = obj.SslCertificates

	return nil
}

func (o *TargetHTTPSProxy) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *TargetHTTPSProxyCreate
	)

	if dest != nil {
		c = dest.(*TargetHTTPSProxyCreate)
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
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(TargetHTTPSProxyName, o.Name),
				append(ops, deleteTargetHTTPSProxyOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(TargetHTTPSProxyName, c.Name),
			append(ops, createTargetHTTPSProxyOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(TargetHTTPSProxyName, c.Name, "forces recreate"),
			append(ops, deleteTargetHTTPSProxyOp(o), createTargetHTTPSProxyOp(c))), nil
	}

	// Check for partial updates.
	if o.URLMap != c.URLMap || !reflect.DeepEqual(o.SSLCertificates, c.SSLCertificates) {
		plan := &TargetHTTPSProxyPlan{
			Name:            o.Name,
			ProjectID:       o.ProjectID,
			URLMap:          o.URLMap,
			SSLCertificates: o.SSLCertificates,
		}

		return types.NewPlanActionUpdate(key, plugin_util.UpdateDesc(TargetHTTPSProxyName, c.Name, "in-place"),
			append(ops, &types.PlanActionOperation{Operation: types.PlanOpUpdate, Steps: 1, Data: plan.Encode()})), nil
	}

	return nil, nil
}

func deleteTargetHTTPSProxyOp(o *TargetHTTPSProxy) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&TargetHTTPSProxyPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createTargetHTTPSProxyOp(c *TargetHTTPSProxyCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&TargetHTTPSProxyPlan{
			Name:            c.Name,
			ProjectID:       c.ProjectID,
			URLMap:          c.URLMap,
			SSLCertificates: c.SSLCertificates,
		}).Encode(),
	}
}

func decodeTargetHTTPSProxyPlan(p *types.PlanActionOperation) (ret *TargetHTTPSProxyPlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func (o *TargetHTTPSProxy) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeTargetHTTPSProxyPlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			oper, err := cli.TargetHttpsProxies.Delete(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(TargetHTTPSProxyName, plan.Name))

		case types.PlanOpAdd:
			// Creation.
			oper, err := cli.TargetHttpsProxies.Insert(plan.ProjectID, &compute.TargetHttpsProxy{
				Name:            plan.Name,
				UrlMap:          plan.URLMap,
				SslCertificates: plan.SSLCertificates,
			}).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(TargetHTTPSProxyName, plan.Name))

			_, err = cli.TargetHttpsProxies.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.Name = plan.Name
			o.ProjectID = plan.ProjectID
			o.URLMap = plan.URLMap
			o.SSLCertificates = plan.SSLCertificates

		case types.PlanOpUpdate:
			oper, err := cli.TargetHttpsProxies.Patch(plan.ProjectID, plan.Name, &compute.TargetHttpsProxy{
				Name:            plan.Name,
				UrlMap:          plan.URLMap,
				SslCertificates: plan.SSLCertificates,
			}).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(TargetHTTPSProxyName, plan.Name))

			_, err = cli.TargetHttpsProxies.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.URLMap = plan.URLMap
			o.SSLCertificates = plan.SSLCertificates
		}
	}

	return nil
}
