package gcp

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type TargetHTTPSProxy struct {
	registry.ResourceBase

	Name            fields.StringInputField `state:"force_new"`
	ProjectID       fields.StringInputField `state:"force_new"`
	URLMap          fields.StringInputField
	SSLCertificates fields.ArrayInputField

	Fingerprint string `state:"-"`
}

func (o *TargetHTTPSProxy) GetName() string {
	return o.Name.Any()
}

func (o *TargetHTTPSProxy) ID() fields.StringInputField {
	return fields.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/targetHttpsProxies/%s", o.ProjectID, o.Name)
}

func (o *TargetHTTPSProxy) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	proxy, err := cli.TargetHttpsProxies.Get(projectID, name).Do()
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

func (o *TargetHTTPSProxy) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()

	oper, err := cli.TargetHttpsProxies.Insert(projectID, o.makeHTTPSProxy()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *TargetHTTPSProxy) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Current()
	name := o.Name.Current()

	// Check fingerprint.
	if o.Fingerprint == "" {
		proxy, err := cli.TargetHttpsProxies.Get(projectID, name).Do()
		if err != nil {
			return err
		}

		o.Fingerprint = proxy.Fingerprint
	}

	oper, err := cli.TargetHttpsProxies.Patch(projectID, name, o.makeHTTPSProxy()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *TargetHTTPSProxy) makeHTTPSProxy() *compute.TargetHttpsProxy {
	var certs []string

	for _, cert := range o.SSLCertificates.Wanted() {
		certs = append(certs, cert.(string))
	}

	return &compute.TargetHttpsProxy{
		Name:            o.Name.Wanted(),
		UrlMap:          o.URLMap.Wanted(),
		SslCertificates: certs,
		Fingerprint:     o.Fingerprint,
	}
}

func (o *TargetHTTPSProxy) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.TargetHttpsProxies.Delete(o.ProjectID.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, o.ProjectID.Current(), oper.Name)
}
