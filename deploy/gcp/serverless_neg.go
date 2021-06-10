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

const ServerlessNEGName = "serverless network endpoint group"

type ServerlessNEG struct {
	Name      string `json:"name"`
	Region    string `json:"region"`
	CloudRun  string `json:"cloud_run" mapstructure:"cloud_run"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`

	Planned *ServerlessNEGCreate `json:"-"`
}

func (o *ServerlessNEG) Key() string {
	return o.CloudRun
}

type ServerlessNEGCreate ServerlessNEG

func (o *ServerlessNEGCreate) Key() string {
	return o.CloudRun
}

func (o *ServerlessNEGCreate) ID() string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/networkEndpointGroups/%s", o.ProjectID, o.Region, o.Name)
}

type ServerlessNEGPlan ServerlessNEG

func (o *ServerlessNEGPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func (o *ServerlessNEG) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ServerlessNEG
		Type string `json:"type"`
	}{
		ServerlessNEG: *o,
		Type:          "gcp_serverless_neg",
	})
}

func (o *ServerlessNEG) verify(cli *compute.Service, c *ServerlessNEGCreate) error {
	name := o.Name
	projectID := o.ProjectID
	region := o.Region

	if name == "" && c != nil {
		name = c.Name
		projectID = c.ProjectID
		region = c.Region
	}

	if name == "" {
		return nil
	}

	obj, err := cli.RegionNetworkEndpointGroups.Get(projectID, region, name).Do()
	if ErrIs404(err) {
		*o = ServerlessNEG{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = projectID
	o.Region = region

	if obj.NetworkEndpointType == "SERVERLESS" {
		if obj.CloudRun != nil {
			o.CloudRun = obj.CloudRun.Service
		}
	}

	return nil
}

func (o *ServerlessNEG) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *ServerlessNEGCreate
	)

	if dest != nil {
		c = dest.(*ServerlessNEGCreate)
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

	o.Planned = c

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(ServerlessNEGName, o.Name),
				append(ops, deleteServerlessNEGOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(ServerlessNEGName, c.Name),
			append(ops, createServerlessNEGOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID || o.Region != c.Region || o.CloudRun != c.CloudRun {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(ServerlessNEGName, c.Name, "forces recreate"),
			append(ops, deleteServerlessNEGOp(o), createServerlessNEGOp(c))), nil
	}

	return nil, nil
}

func deleteServerlessNEGOp(o *ServerlessNEG) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&ServerlessNEGPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
			Region:    o.Region,
		}).Encode(),
	}
}

func createServerlessNEGOp(c *ServerlessNEGCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&ServerlessNEGPlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
			Region:    c.Region,
			CloudRun:  c.CloudRun,
		}).Encode(),
	}
}

func decodeNetworkEndpointGroupPlan(p *types.PlanActionOperation) (ret *ServerlessNEGPlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func (o *ServerlessNEG) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeNetworkEndpointGroupPlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			op, err := cli.RegionNetworkEndpointGroups.Delete(plan.ProjectID, plan.Region, plan.Name).Do()
			if err != nil {
				return err
			}

			err = waitForRegionOperation(cli, plan.ProjectID, plan.Region, op.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(ServerlessNEGName, plan.Name))

		case types.PlanOpAdd:
			// Creation.
			op, err := cli.RegionNetworkEndpointGroups.Insert(plan.ProjectID, plan.Region, &compute.NetworkEndpointGroup{
				Name:                plan.Name,
				NetworkEndpointType: "SERVERLESS",
				CloudRun: &compute.NetworkEndpointGroupCloudRun{
					Service: plan.CloudRun,
				},
			}).Do()
			if err != nil {
				return err
			}

			err = waitForRegionOperation(cli, plan.ProjectID, plan.Region, op.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(ServerlessNEGName, plan.Name))

			_, err = cli.RegionNetworkEndpointGroups.Get(plan.ProjectID, plan.Region, plan.Name).Do()
			if err != nil {
				return err
			}

			o.Name = plan.Name
			o.ProjectID = plan.ProjectID
			o.Region = plan.Region
			o.CloudRun = plan.CloudRun

		case types.PlanOpUpdate:
			return fmt.Errorf("unimplemented")
		}
	}

	return nil
}
