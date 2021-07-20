package deploy

import (
	"context"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
	"google.golang.org/api/compute/v1"
)

type CacheInvalidate struct {
	registry.ResourceBase

	URLMapName fields.StringInputField
	ProjectID  fields.StringInputField
	StaticApps []*StaticApp `state:"-"`

	changedStaticApps []*StaticApp
}

func (o *CacheInvalidate) GetName() string {
	return o.URLMapName.Any()
}

func (o *CacheInvalidate) SkipState() bool {
	return true
}

func anyFileChanged(files []*gcp.BucketObject) bool {
	for _, f := range files {
		if f.Hash.IsChanged() {
			return true
		}
	}

	return false
}

func (o *CacheInvalidate) CalculateDiff() registry.DiffType {
	for _, app := range o.StaticApps {
		if app.Opts.CDN.Enabled && anyFileChanged(app.Files) {
			o.changedStaticApps = append(o.changedStaticApps, app)
		}
	}

	if len(o.changedStaticApps) == 0 {
		return registry.DiffTypeNone
	}

	return registry.DiffTypeProcess
}

func (o *CacheInvalidate) FieldDependencies() []interface{} {
	ret := make([]interface{}, 0)

	for _, app := range o.StaticApps {
		for _, f := range app.Files {
			ret = append(ret, f.Name)
		}
	}

	return ret
}

func (o *CacheInvalidate) Process(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	urlMap := o.URLMapName.Wanted()
	g, _ := errgroup.WithConcurrency(ctx, gcp.DefaultConcurrency)

	for _, app := range o.changedStaticApps {
		host, path := gcp.SplitURL(app.App.URL)

		if strings.HasSuffix(path, "/") {
			path += "*"
		}

		g.Go(func() error {
			oper, err := cli.UrlMaps.InvalidateCache(projectID, urlMap, &compute.CacheInvalidationRule{
				Host: host,
				Path: path,
			}).Do()
			if err != nil {
				return err
			}

			return gcp.WaitForGlobalComputeOperation(cli, projectID, oper.Name)
		})
	}

	return g.Wait()
}
