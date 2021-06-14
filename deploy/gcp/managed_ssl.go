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

const ManagedSSLName = "SSL certificate"

type ManagedSSL struct {
	Name      string `json:"name"`
	Domain    string `json:"domain"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`
}

func (o *ManagedSSL) Key() string {
	return o.Domain
}

func (o *ManagedSSL) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ManagedSSL
		Type string `json:"type"`
	}{
		ManagedSSL: *o,
		Type:       "gcp_managed_ssl",
	})
}

type ManagedSSLCreate ManagedSSL

func (o *ManagedSSLCreate) Key() string {
	return o.Domain
}

type ManagedSSLPlan ManagedSSL

func (o *ManagedSSLPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}
func (o *ManagedSSL) verify(cli *compute.Service, c *ManagedSSLCreate) error {
	name := o.Name
	projectID := o.ProjectID

	if name == "" && c != nil {
		name = c.Name
		projectID = c.ProjectID
	}

	if name == "" {
		return nil
	}

	obj, err := cli.SslCertificates.Get(projectID, name).Do()
	if ErrIs404(err) {
		*o = ManagedSSL{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = projectID

	if obj.Managed != nil && len(obj.Managed.Domains) == 1 {
		o.Domain = obj.Managed.Domains[0]
	}

	return nil
}

func (o *ManagedSSL) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *ManagedSSLCreate
	)

	if dest != nil {
		c = dest.(*ManagedSSLCreate)
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
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(ManagedSSLName, o.Name),
				append(ops, deleteManagedSSLOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(ManagedSSLName, c.Name),
			append(ops, createManagedSSLOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(ManagedSSLName, c.Name, "forces recreate"),
			append(ops, deleteManagedSSLOp(o), createManagedSSLOp(c))), nil
	}

	return nil, nil
}

func deleteManagedSSLOp(o *ManagedSSL) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&ManagedSSLPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createManagedSSLOp(c *ManagedSSLCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&ManagedSSLPlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
			Domain:    c.Domain,
		}).Encode(),
	}
}

func decodeManagedSSLPlan(p *types.PlanActionOperation) (ret *ManagedSSLPlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func (o *ManagedSSL) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeManagedSSLPlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			oper, err := cli.SslCertificates.Delete(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(ManagedSSLName, plan.Name))

		case types.PlanOpAdd:
			// Creation.
			oper, err := cli.SslCertificates.Insert(plan.ProjectID, &compute.SslCertificate{
				Name: plan.Name,
				Type: "MANAGED",
				Managed: &compute.SslCertificateManagedSslCertificate{
					Domains: []string{plan.Domain},
				},
			}).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(ManagedSSLName, plan.Name))

			_, err = cli.SslCertificates.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.Name = plan.Name
			o.ProjectID = plan.ProjectID
			o.Domain = plan.Domain

		case types.PlanOpUpdate:
			return fmt.Errorf("unimplemented")
		}
	}

	return nil
}
