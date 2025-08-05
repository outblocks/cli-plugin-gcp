package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/serviceusage/v1"
)

type APIService struct {
	registry.ResourceBase

	ProjectNumber fields.IntInputField    `state:"force_new"`
	Name          fields.StringInputField `state:"force_new"`
}

func (o *APIService) ReferenceID() string {
	return fields.GenerateID("projects/%d/services/%s", o.ProjectNumber, o.Name)
}

func (o *APIService) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *APIService) Read(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck
	name := o.Name.Any()

	apiCli, err := pctx.GCPServiceUsageClient(ctx)
	if err != nil {
		return err
	}

	id := fmt.Sprintf("projects/%d/services/%s", o.ProjectNumber.Any(), name)

	res, err := apiCli.Services.Get(id).Do()
	if err != nil {
		return err
	}

	if res.State != "ENABLED" {
		o.MarkAsNew()
	} else {
		o.MarkAsExisting()
	}

	o.Name.SetCurrent(name)

	return nil
}

func (o *APIService) Create(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPServiceUsageClient(ctx)
	if err != nil {
		return err
	}

	id := fmt.Sprintf("projects/%d/services/%s", o.ProjectNumber.Wanted(), o.Name.Wanted())

	op, err := cli.Services.Enable(id, &serviceusage.EnableServiceRequest{}).Do()
	if err != nil {
		return err
	}

	err = WaitForServiceUsageOperation(ctx, cli, op)
	if err != nil {
		return err
	}

	return nil
}

func (o *APIService) Update(_ context.Context, _ any) error {
	return fmt.Errorf("unimplemented")
}

func (o *APIService) Delete(_ context.Context, _ any) error {
	return nil
}
