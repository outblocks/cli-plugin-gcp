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

	Name fields.StringInputField `state:"force_new"`
}

func (o *APIService) GetName() string {
	return o.Name.Any()
}

func (o *APIService) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)
	name := o.Name.Any()

	apiCli, err := pctx.GCPServiceUsageClient(ctx)
	if err != nil {
		return err
	}

	res, err := apiCli.Services.Get(fmt.Sprintf("projects/%d/services/%s", pctx.Settings().ProjectNumber, name)).Do()
	if err != nil {
		return err
	}

	if res.State != "ENABLED" {
		o.SetNew(true)
	}

	o.SetNew(false)
	o.Name.SetCurrent(name)

	return nil
}

func (o *APIService) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPServiceUsageClient(ctx)
	if err != nil {
		return err
	}

	op, err := cli.Services.Enable(fmt.Sprintf("projects/%d/services/%s", pctx.Settings().ProjectNumber, o.Name.Wanted()), &serviceusage.EnableServiceRequest{}).Do()
	if err != nil {
		return err
	}

	err = waitForServiceUsageOperation(cli, op)
	if err != nil {
		return err
	}

	return nil
}

func (o *APIService) Update(ctx context.Context, meta interface{}) error {
	return fmt.Errorf("unimplemented")
}

func (o *APIService) Delete(ctx context.Context, meta interface{}) error {
	return nil
}
