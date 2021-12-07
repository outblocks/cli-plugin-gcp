package gcp

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type TargetHTTPProxy struct {
	registry.ResourceBase

	Name      fields.StringInputField `state:"force_new"`
	ProjectID fields.StringInputField `state:"force_new"`
	URLMap    fields.StringInputField

	Fingerprint string `state:"-"`
}

func (o *TargetHTTPProxy) ReferenceID() string {
	return fields.GenerateID("projects/%s/global/targetHttpProxies/%s", o.ProjectID, o.Name)
}

func (o *TargetHTTPProxy) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *TargetHTTPProxy) RefField() fields.StringInputField {
	return fields.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/targetHttpProxies/%s", o.ProjectID, o.Name)
}

func (o *TargetHTTPProxy) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	proxy, err := cli.TargetHttpProxies.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)
	o.URLMap.SetCurrent(proxy.UrlMap)

	o.Fingerprint = proxy.Fingerprint

	return nil
}

func (o *TargetHTTPProxy) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()

	oper, err := cli.TargetHttpProxies.Insert(projectID, &compute.TargetHttpProxy{
		Name:   name,
		UrlMap: o.URLMap.Wanted(),
	}).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *TargetHTTPProxy) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Current()
	name := o.Name.Current()

	// Check fingerprint.
	if o.Fingerprint == "" {
		proxy, err := cli.TargetHttpProxies.Get(projectID, name).Do()
		if err != nil {
			return err
		}

		o.Fingerprint = proxy.Fingerprint
	}

	oper, err := cli.TargetHttpProxies.Patch(projectID, name, &compute.TargetHttpProxy{
		Name:        name,
		UrlMap:      o.URLMap.Wanted(),
		Fingerprint: o.Fingerprint,
	}).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *TargetHTTPProxy) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.TargetHttpProxies.Delete(o.ProjectID.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, o.ProjectID.Current(), oper.Name)
}
