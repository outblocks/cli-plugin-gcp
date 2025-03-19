package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type Address struct {
	registry.ResourceBase

	Name      fields.StringInputField `state:"force_new"`
	ProjectID fields.StringInputField `state:"force_new"`
	IP        fields.StringOutputField
}

func (o *Address) ReferenceID() string {
	return fields.GenerateID("projects/%s/global/addresses/%s", o.ProjectID, o.Name)
}

func (o *Address) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *Address) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	addr, err := cli.GlobalAddresses.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)
	o.IP.SetCurrent(addr.Address)

	return nil
}

func (o *Address) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()

	oper, err := cli.GlobalAddresses.Insert(projectID, &compute.Address{
		Name: name,
	}).Do()
	if err != nil {
		return err
	}

	err = WaitForGlobalComputeOperation(cli, projectID, oper.Name)
	if err != nil {
		return err
	}

	addr, err := cli.GlobalAddresses.Get(projectID, name).Do()
	if err != nil {
		return err
	}

	o.IP.SetCurrent(addr.Address)

	return nil
}

func (o *Address) Update(_ context.Context, _ interface{}) error {
	return fmt.Errorf("unimplemented")
}

func (o *Address) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.GlobalAddresses.Delete(o.ProjectID.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, o.ProjectID.Current(), oper.Name)
}
