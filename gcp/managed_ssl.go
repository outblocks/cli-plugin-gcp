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

	Name         fields.StringInputField `state:"force_new"`
	ProjectID    fields.StringInputField `state:"force_new"`
	Domains      fields.ArrayInputField  `state:"force_new"`
	Status       fields.StringOutputField
	DomainStatus fields.MapOutputField
}

func (o *ManagedSSL) ReferenceID() string {
	return fields.GenerateID("projects/%s/global/sslCertificates/%s", o.ProjectID, o.Name)
}

func (o *ManagedSSL) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *ManagedSSL) RefField() fields.StringInputField {
	return fields.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/sslCertificates/%s", o.ProjectID, o.Name)
}

func (o *ManagedSSL) Init(ctx context.Context, meta any, opts *registry.Options) error {
	// Make sure managed ssl status is read always if ssl already exists.
	if opts.Read || !o.IsExisting() {
		return nil
	}

	return o.Read(ctx, meta)
}

func (o *ManagedSSL) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	cert, err := cli.SslCertificates.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.Status.Invalidate()
		o.DomainStatus.Invalidate()
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)

	if cert.Managed != nil {
		domains := make([]any, len(cert.Managed.Domains))
		for i, v := range cert.Managed.Domains {
			domains[i] = v
		}

		o.Domains.SetCurrent(domains)
		o.Status.SetCurrent(cert.Managed.Status)

		domainStatus := make(map[string]any)
		for k, v := range cert.Managed.DomainStatus {
			domainStatus[k] = v
		}

		o.DomainStatus.SetCurrent(domainStatus)
	}

	return nil
}

func (o *ManagedSSL) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()
	domains := o.Domains.Wanted()
	domainsStr := make([]string, len(domains))

	for i, v := range domains {
		domainsStr[i] = v.(string) //nolint:errcheck
	}

	oper, err := cli.SslCertificates.Insert(projectID, &compute.SslCertificate{
		Name: name,
		Type: "MANAGED",
		Managed: &compute.SslCertificateManagedSslCertificate{
			Domains: domainsStr,
		},
	}).Do()
	if err != nil {
		return err
	}

	err = WaitForGlobalComputeOperation(cli, projectID, oper.Name)
	if err != nil {
		return err
	}

	cert, err := cli.SslCertificates.Get(projectID, name).Do()
	if err != nil {
		return err
	}

	o.Status.SetCurrent(cert.Managed.Status)

	domainStatus := make(map[string]any)
	for k, v := range cert.Managed.DomainStatus {
		domainStatus[k] = v
	}

	o.DomainStatus.SetCurrent(domainStatus)

	return nil
}

func (o *ManagedSSL) Update(_ context.Context, _ any) error {
	return fmt.Errorf("unimplemented")
}

func (o *ManagedSSL) Delete(ctx context.Context, meta any) error {
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
