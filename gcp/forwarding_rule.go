package gcp

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type ForwardingRule struct {
	registry.ResourceBase

	Name      fields.StringInputField `state:"force_new"`
	ProjectID fields.StringInputField `state:"force_new"`
	IPAddress fields.StringInputField `state:"force_new"`
	Target    fields.StringInputField
	PortRange fields.StringInputField

	Fingerprint string `state:"-"`
}

func (o *ForwardingRule) ReferenceID() string {
	return fields.GenerateID("projects/%s/global/forwardingRules/%s", o.ProjectID, o.Name)
}

func (o *ForwardingRule) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *ForwardingRule) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	rule, err := cli.GlobalForwardingRules.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)
	o.IPAddress.SetCurrent(rule.IPAddress)
	o.Target.SetCurrent(rule.Target)
	o.PortRange.SetCurrent(rule.PortRange)

	o.Fingerprint = rule.Fingerprint

	return nil
}

func (o *ForwardingRule) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()

	oper, err := cli.GlobalForwardingRules.Insert(projectID, &compute.ForwardingRule{
		Name:      name,
		IPAddress: o.IPAddress.Wanted(),
		Target:    o.Target.Wanted(),
		PortRange: o.PortRange.Wanted(),
	}).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *ForwardingRule) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Current()
	name := o.Name.Current()

	// Check fingerprint.
	if o.Fingerprint == "" {
		rule, err := cli.GlobalForwardingRules.Get(projectID, name).Do()
		if err != nil {
			return err
		}

		o.Fingerprint = rule.Fingerprint
	}

	oper, err := cli.GlobalForwardingRules.Patch(projectID, name, &compute.ForwardingRule{
		Name:        name,
		IPAddress:   o.IPAddress.Wanted(),
		Target:      o.Target.Wanted(),
		PortRange:   o.PortRange.Wanted(),
		Fingerprint: o.Fingerprint,
	}).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *ForwardingRule) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.GlobalForwardingRules.Delete(o.ProjectID.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, o.ProjectID.Current(), oper.Name)
}
