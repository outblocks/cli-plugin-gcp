package deploy

import (
	"context"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
	"google.golang.org/api/compute/v1"
)

type CacheInvalidate struct {
	registry.ResourceBase

	URLMapName   fields.StringInputField
	ProjectID    fields.StringInputField
	StaticApps   []*StaticApp   `state:"-"`
	ServiceApps  []*ServiceApp  `state:"-"`
	FunctionApps []*FunctionApp `state:"-"`

	changedURLs []string
}

func (o *CacheInvalidate) GetName() string {
	return fields.VerboseString(o.URLMapName)
}

func (o *CacheInvalidate) SkipState() bool {
	return true
}

func anyFileChanged(files ...*gcp.BucketObject) bool {
	for _, f := range files {
		if f.Hash.IsChanged() && !f.IsNew() {
			return true
		}
	}

	return false
}

func (o *CacheInvalidate) CalculateDiff(context.Context, interface{}) (registry.DiffType, error) {
	for _, app := range o.StaticApps {
		if app.Props.CDN.Enabled && anyFileChanged(app.Files...) {
			o.changedURLs = append(o.changedURLs, app.App.Url)
		}
	}

	for _, app := range o.ServiceApps {
		if app.Props.CDN.Enabled && app.Image.Digest.IsChanged() {
			o.changedURLs = append(o.changedURLs, app.App.Url)
		}
	}

	for _, app := range o.FunctionApps {
		if app.Props.CDN.Enabled && anyFileChanged(app.Archive) {
			o.changedURLs = append(o.changedURLs, app.App.Url)
		}
	}

	if len(o.changedURLs) == 0 {
		return registry.DiffTypeNone, nil
	}

	return registry.DiffTypeProcess, nil
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

	for _, url := range o.changedURLs {
		host, path := gcp.SplitURL(url)

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
