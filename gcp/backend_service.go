package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type BackendService struct {
	registry.ResourceBase

	Name      fields.StringInputField `state:"force_new"`
	ProjectID fields.StringInputField `state:"force_new"`
	NEG       fields.StringInputField

	CDN struct {
		Enabled        fields.BoolInputField
		CacheMode      fields.StringInputField `default:"CACHE_ALL_STATIC"`
		CacheKeyPolicy struct {
			IncludeHost        fields.BoolInputField `default:"1"`
			IncludeProtocol    fields.BoolInputField `default:"1"`
			IncludeQueryString fields.BoolInputField `default:"1"`
		}
		DefaultTTL fields.IntInputField `default:"3600"`
		MaxTTL     fields.IntInputField `default:"86400"`
		ClientTTL  fields.IntInputField `default:"3600"`
	}
}

func (o *BackendService) GetName() string {
	return o.Name.Any()
}

func (o *BackendService) ID() fields.StringInputField {
	return fields.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/backendServices/%s", o.ProjectID, o.Name)
}

func (o *BackendService) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	svc, err := cli.BackendServices.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)

	if len(svc.Backends) == 1 {
		o.NEG.SetCurrent(svc.Backends[0].Group)
	}

	o.CDN.Enabled.SetCurrent(svc.EnableCDN)

	if svc.CdnPolicy != nil {
		o.CDN.CacheMode.SetCurrent(svc.CdnPolicy.CacheMode)
		o.CDN.DefaultTTL.SetCurrent(int(svc.CdnPolicy.DefaultTtl))
		o.CDN.MaxTTL.SetCurrent(int(svc.CdnPolicy.MaxTtl))
		o.CDN.ClientTTL.SetCurrent(int(svc.CdnPolicy.ClientTtl))

		if svc.CdnPolicy.CacheKeyPolicy != nil {
			o.CDN.CacheKeyPolicy.IncludeHost.SetCurrent(svc.CdnPolicy.CacheKeyPolicy.IncludeHost)
			o.CDN.CacheKeyPolicy.IncludeProtocol.SetCurrent(svc.CdnPolicy.CacheKeyPolicy.IncludeProtocol)
			o.CDN.CacheKeyPolicy.IncludeQueryString.SetCurrent(svc.CdnPolicy.CacheKeyPolicy.IncludeQueryString)
		}
	}

	return nil
}

func (o *BackendService) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()

	oper, err := cli.BackendServices.Insert(projectID, &compute.BackendService{
		Name:      name,
		EnableCDN: o.CDN.Enabled.Wanted(),
		CdnPolicy: &compute.BackendServiceCdnPolicy{
			CacheMode: o.CDN.CacheMode.Wanted(),
			CacheKeyPolicy: &compute.CacheKeyPolicy{
				IncludeHost:        o.CDN.CacheKeyPolicy.IncludeHost.Wanted(),
				IncludeProtocol:    o.CDN.CacheKeyPolicy.IncludeProtocol.Wanted(),
				IncludeQueryString: o.CDN.CacheKeyPolicy.IncludeQueryString.Wanted(),
			},
			DefaultTtl: int64(o.CDN.DefaultTTL.Wanted()),
			MaxTtl:     int64(o.CDN.MaxTTL.Wanted()),
			ClientTtl:  int64(o.CDN.ClientTTL.Wanted()),
		},
		Backends: []*compute.Backend{
			{
				Group: o.NEG.Wanted(),
			},
		},
	}).Do()
	if err != nil {
		return err
	}

	return waitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *BackendService) Update(ctx context.Context, meta interface{}) error {
	return fmt.Errorf("unimplemented")
}

func (o *BackendService) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.BackendServices.Delete(o.ProjectID.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return waitForGlobalComputeOperation(cli, o.ProjectID.Current(), oper.Name)
}
