package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type SelfManagedSSL struct {
	registry.ResourceBase

	Name        fields.StringInputField `state:"force_new"`
	ProjectID   fields.StringInputField `state:"force_new"`
	Certificate fields.StringInputField `state:"force_new"`
	PrivateKey  fields.StringInputField `state:"force_new"`
}

func (o *SelfManagedSSL) ReferenceID() string {
	return fields.GenerateID("projects/%s/global/sslCertificates/%s", o.ProjectID, o.Name)
}

func (o *SelfManagedSSL) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *SelfManagedSSL) RefField() fields.StringInputField {
	return fields.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/sslCertificates/%s", o.ProjectID, o.Name)
}

func (o *SelfManagedSSL) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

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

	if cert.SelfManaged != nil {
		o.Certificate.SetCurrent(cert.Certificate)
	}

	return nil
}

func (o *SelfManagedSSL) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()

	oper, err := cli.SslCertificates.Insert(projectID, &compute.SslCertificate{
		Name: name,
		Type: "SELF_MANAGED",
		SelfManaged: &compute.SslCertificateSelfManagedSslCertificate{
			Certificate: o.Certificate.Wanted(),
			PrivateKey:  o.PrivateKey.Wanted(),
		},
	}).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *SelfManagedSSL) Update(_ context.Context, _ any) error {
	return fmt.Errorf("unimplemented")
}

func (o *SelfManagedSSL) Delete(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.SslCertificates.Delete(o.ProjectID.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, o.ProjectID.Current(), oper.Name)
}
