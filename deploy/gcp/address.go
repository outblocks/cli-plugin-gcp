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

const AddressName = "IP address"

type Address struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`
	IP        string `json:"ip"`
}

func (o *Address) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Address
		Type string `json:"type"`
	}{
		Address: *o,
		Type:    "gcp_address",
	})
}

type AddressCreate struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`
}

type AddressPlan AddressCreate

func (o *AddressPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func (o *Address) verify(cli *compute.Service, c *AddressCreate) error {
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
		*o = Address{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = projectID
	o.IP = addr.Address

	return nil
}

func (o *Address) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *AddressCreate
	)

	if dest != nil {
		c = dest.(*AddressCreate)
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
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(AddressName, o.Name),
				append(ops, deleteAddressOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(AddressName, c.Name),
			append(ops, createAddressOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(AddressName, c.Name, "forces recreate"),
			append(ops, deleteAddressOp(o), createAddressOp(c))), nil
	}

	return nil, nil
}

func deleteAddressOp(o *Address) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&AddressPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createAddressOp(c *AddressCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&AddressPlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
		}).Encode(),
	}
}

func (o *Address) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeCloudRunPlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			oper, err := cli.GlobalAddresses.Delete(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(AddressName, plan.Name))

		case types.PlanOpAdd:
			// Creation.
			oper, err := cli.GlobalAddresses.Insert(plan.ProjectID, &compute.Address{
				Name: plan.Name,
			}).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(AddressName, plan.Name))

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
