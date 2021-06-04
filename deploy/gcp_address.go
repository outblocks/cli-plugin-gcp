package deploy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/compute/v1"
)

type GCPAddress struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`
	IP        string `json:"ip"`
}

type GCPAddressCreate struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`
}

type GCPAddressPlan GCPAddressCreate

func (o *GCPAddressPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func (o *GCPAddress) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPAddress
		Type string `json:"type"`
	}{
		GCPAddress: *o,
		Type:       "gcp_address",
	})
}

func (o *GCPAddress) verify(cli *compute.Service, c *GCPAddressCreate) error {
	name := o.Name
	projectID := o.ProjectID

	if name == "" && c != nil {
		name = c.Name
		projectID = c.ProjectID
	}

	if name == "" {
		return nil
	}

	addr, err := cli.GlobalAddresses.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.Name = ""

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = projectID
	o.IP = addr.Address

	return nil
}

func (o *GCPAddress) Plan(ctx context.Context, cli *compute.Service, c *GCPAddressCreate, verify bool) (*types.PlanAction, error) {
	var ops []*types.PlanActionOperation

	if verify {
		err := o.verify(cli, c)
		if err != nil {
			return nil, err
		}
	}

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(plugin_util.DeleteDesc("IP address", o.Name),
				append(ops, deleteGCPAddressOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(plugin_util.AddDesc("IP address", c.Name),
			append(ops, createGCPAddressOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID {
		return types.NewPlanActionRecreate(plugin_util.UpdateDesc("IP address", c.Name, "forces recreate"),
			append(ops, deleteGCPAddressOp(o), createGCPAddressOp(c))), nil
	}

	return nil, nil
}

func deleteGCPAddressOp(o *GCPAddress) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&GCPAddressPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createGCPAddressOp(c *GCPAddressCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     2,
		Operation: types.PlanOpAdd,
		Data: (&GCPAddressPlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
		}).Encode(),
	}
}

func (o *GCPAddress) Apply(ctx context.Context, cli *compute.Service, a *types.PlanAction, callback func(desc string)) error {
	// Process operations.
	for _, p := range a.Operations {
		plan, err := decodeGCPCloudRunPlan(p)
		if err != nil {
			return err
		}

		switch p.Operation {
		case types.PlanOpDelete:
			// Deletion.
			_, err = cli.GlobalAddresses.Delete(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc("IP address", plan.Name))

		case types.PlanOpAdd:
			// Creation.
			op, err := cli.GlobalAddresses.Insert(plan.ProjectID, &compute.Address{
				Name: plan.Name,
			}).Do()
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc("IP address", plan.Name))

			err = waitForGlobalOperation(cli, plan.ProjectID, op.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc("IP address", plan.Name, "ready"))

			addr, err := cli.GlobalAddresses.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.Name = plan.Name
			o.ProjectID = plan.ProjectID
			o.IP = addr.Address

		case types.PlanOpUpdate:
			return fmt.Errorf("unimplemented")
		}
	}

	return nil
}

func (o *GCPAddress) planner(ctx context.Context, cli *compute.Service, c *GCPAddressCreate, verify bool) func() (*types.PlanAction, error) {
	return func() (*types.PlanAction, error) {
		return o.Plan(ctx, cli, c, verify)
	}
}

func (o *GCPAddress) applier(ctx context.Context, cli *compute.Service) func(*types.PlanAction, util.ApplyCallbackFunc) error {
	return func(a *types.PlanAction, cb util.ApplyCallbackFunc) error {
		return o.Apply(ctx, cli, a, cb)
	}
}
