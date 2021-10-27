package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type ServerlessNEG struct {
	registry.ResourceBase

	Name      fields.StringInputField `state:"force_new"`
	ProjectID fields.StringInputField `state:"force_new"`
	Region    fields.StringInputField `state:"force_new"`
	CloudRun  fields.StringInputField `state:"force_new"`
}

func (o *ServerlessNEG) UniqueID() string {
	return fields.GenerateID("projects/%s/regions/%s/networkEndpointGroups/%s", o.ProjectID, o.Region, o.Name)
}

func (o *ServerlessNEG) GetName() string {
	return o.Name.Any()
}

func (o *ServerlessNEG) RefField() fields.StringInputField {
	return fields.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/networkEndpointGroups/%s", o.ProjectID, o.Region, o.Name)
}

func (o *ServerlessNEG) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	region := o.Region.Any()
	name := o.Name.Any()

	neg, err := cli.RegionNetworkEndpointGroups.Get(projectID, region, name).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Region.SetCurrent(region)
	o.Name.SetCurrent(name)

	if neg.CloudRun != nil {
		o.CloudRun.SetCurrent(neg.CloudRun.Service)
	}

	return nil
}

func (o *ServerlessNEG) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	region := o.Region.Wanted()
	name := o.Name.Wanted()
	cloudRun := o.CloudRun.Wanted()

	oper, err := cli.RegionNetworkEndpointGroups.Insert(projectID, region, &compute.NetworkEndpointGroup{
		Name:                name,
		NetworkEndpointType: "SERVERLESS",
		CloudRun: &compute.NetworkEndpointGroupCloudRun{
			Service: cloudRun,
		},
	}).Do()
	if err != nil {
		return err
	}

	return WaitForRegionComputeOperation(cli, projectID, region, oper.Name)
}

func (o *ServerlessNEG) Update(ctx context.Context, meta interface{}) error {
	return fmt.Errorf("unimplemented")
}

func (o *ServerlessNEG) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.RegionNetworkEndpointGroups.Delete(o.ProjectID.Current(), o.Region.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForRegionComputeOperation(cli, o.ProjectID.Current(), o.Region.Current(), oper.Name)
}
