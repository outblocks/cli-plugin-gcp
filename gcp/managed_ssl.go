package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type ManagedSSL struct {
	registry.ResourceBase

	Name      fields.StringInputField `state:"force_new"`
	ProjectID fields.StringInputField `state:"force_new"`
	Domain    fields.StringInputField `state:"force_new"`
}

func (o *ManagedSSL) GetName() string {
	return o.Domain.Any()
}

func (o *ManagedSSL) ID() fields.StringInputField {
	return fields.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/sslCertificates/%s", o.ProjectID, o.Name)
}

func (o *ManagedSSL) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	cert, err := cli.SslCertificates.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)

	if cert.Managed != nil && len(cert.Managed.Domains) == 1 {
		o.Domain.SetCurrent(cert.Managed.Domains[0])
	}

	return nil
}

func (o *ManagedSSL) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()
	domain := o.Domain.Wanted()

	oper, err := cli.SslCertificates.Insert(projectID, &compute.SslCertificate{
		Name: name,
		Type: "MANAGED",
		Managed: &compute.SslCertificateManagedSslCertificate{
			Domains: []string{domain},
		},
	}).Do()
	if err != nil {
		return err
	}

	return waitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *ManagedSSL) Update(ctx context.Context, meta interface{}) error {
	return fmt.Errorf("unimplemented")
}

func (o *ManagedSSL) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.SslCertificates.Delete(o.ProjectID.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return waitForGlobalComputeOperation(cli, o.ProjectID.Current(), oper.Name)
}
