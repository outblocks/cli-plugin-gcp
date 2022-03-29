package gcp

import (
	"context"
	"sort"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/compute/v1"
)

type URLMap struct {
	registry.ResourceBase

	Name       fields.StringInputField `state:"force_new"`
	ProjectID  fields.StringInputField `state:"force_new"`
	URLMapping fields.MapInputField    `state:"propagate_recreate"`
	AppMapping fields.MapInputField

	Fingerprint string `state:"-"`
}

func (o *URLMap) ReferenceID() string {
	return fields.GenerateID("projects/%s/global/urlMaps/%s", o.ProjectID, o.Name)
}

func (o *URLMap) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *URLMap) RefField() fields.StringInputField {
	return fields.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/urlMaps/%s", o.ProjectID, o.Name)
}

func (o *URLMap) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	obj, err := cli.UrlMaps.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return err
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)

	// Read URL mapping.
	urlMap := make(map[string]interface{})
	pathMatchersMap := make(map[string]*compute.PathMatcher, len(obj.PathMatchers))

	for _, pm := range obj.PathMatchers {
		pathMatchersMap[pm.Name] = pm
	}

	for _, hr := range obj.HostRules {
		for _, host := range hr.Hosts {
			pm := pathMatchersMap[hr.PathMatcher]
			pathRedirect := ""

			if pm.DefaultRouteAction != nil && pm.DefaultRouteAction.UrlRewrite != nil {
				pathRedirect = pm.DefaultRouteAction.UrlRewrite.PathPrefixRewrite
			}

			urlMap[host+"/*"] = map[string]interface{}{
				URLPathMatcherServiceIDKey:         pm.DefaultService,
				URLPathMatcherPathPrefixRewriteKey: pathRedirect,
			}

			for _, pr := range pm.PathRules {
				for _, p := range pr.Paths {
					urlMap[host+p] = map[string]interface{}{
						URLPathMatcherServiceIDKey:         pr.Service,
						URLPathMatcherPathPrefixRewriteKey: pathRedirect,
					}
				}
			}
		}
	}

	o.URLMapping.SetCurrent(urlMap)
	o.Fingerprint = obj.Fingerprint

	return nil
}

func (o *URLMap) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()

	oper, err := cli.UrlMaps.Insert(projectID, o.MakeURLMap()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *URLMap) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Current()
	name := o.Name.Current()

	// Check fingerprint.
	if o.Fingerprint == "" {
		obj, err := cli.UrlMaps.Get(projectID, name).Do()
		if err != nil {
			return err
		}

		o.Fingerprint = obj.Fingerprint
	}

	oper, err := cli.UrlMaps.Update(projectID, name, o.MakeURLMap()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, projectID, oper.Name)
}

func (o *URLMap) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient(ctx)
	if err != nil {
		return err
	}

	oper, err := cli.UrlMaps.Delete(o.ProjectID.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForGlobalComputeOperation(cli, o.ProjectID.Current(), oper.Name)
}

type URLMapping struct {
	Host        string
	PathMatcher []*URLPathMatcher
}

type URLPathMatcher struct {
	Paths             []string
	ServiceID         string
	PathPrefixRewrite string
}

const (
	URLPathMatcherServiceIDKey         = "service_id"
	URLPathMatcherPathPrefixRewriteKey = "path_redirect"
)

func (o *URLMap) cleanupURLMapping() []*URLMapping {
	m := o.URLMapping.Wanted()

	hmap := make(map[string][]*URLPathMatcher)

	for k, v := range m {
		host, path := SplitURL(k)
		valMap := v.(map[string]interface{})

		hmap[host] = append(hmap[host], &URLPathMatcher{
			Paths:             []string{path},
			ServiceID:         valMap[URLPathMatcherServiceIDKey].(string),
			PathPrefixRewrite: valMap[URLPathMatcherPathPrefixRewriteKey].(string),
		})
	}

	ret := make([]*URLMapping, 0, len(hmap))

	for k, v := range hmap {
		sort.Slice(v, func(i, j int) bool {
			var shortest1, shortest2 string

			for _, p := range v[i].Paths {
				if p == "/*" {
					return true
				}

				if shortest1 == "" || len(shortest1) > len(p) {
					shortest1 = p
				}
			}

			for _, p := range v[j].Paths {
				if p == "/*" {
					return false
				}

				if shortest2 == "" || len(shortest2) > len(p) {
					shortest2 = p
				}
			}

			return shortest1 < shortest2
		})

		ret = append(ret, &URLMapping{
			Host:        k,
			PathMatcher: v,
		})
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Host < ret[j].Host
	})

	return ret
}

func (o *URLMap) MakeURLMap() *compute.UrlMap {
	name := o.Name.Wanted()

	urlMap := &compute.UrlMap{
		Name:        name,
		Fingerprint: o.Fingerprint,
	}

	mapping := o.cleanupURLMapping()

	for _, matcher := range mapping {
		host := matcher.Host

		urlMap.HostRules = append(urlMap.HostRules, &compute.HostRule{
			Hosts:       []string{host},
			PathMatcher: GenericID(name, host),
		})

		pathMatcher := &compute.PathMatcher{
			Name: GenericID(name, host),
		}

		urlMap.PathMatchers = append(urlMap.PathMatchers, pathMatcher)

		for _, m := range matcher.PathMatcher {
			if len(m.Paths) == 1 && m.Paths[0] == "/*" {
				pathMatcher.DefaultService = m.ServiceID
				pathMatcher.DefaultRouteAction = &compute.HttpRouteAction{
					UrlRewrite: &compute.UrlRewrite{
						PathPrefixRewrite: m.PathPrefixRewrite,
					},
				}

				continue
			}

			pathMatcher.PathRules = append(pathMatcher.PathRules, &compute.PathRule{
				Paths:   m.Paths,
				Service: m.ServiceID,
				RouteAction: &compute.HttpRouteAction{
					UrlRewrite: &compute.UrlRewrite{
						PathPrefixRewrite: m.PathPrefixRewrite,
					},
				},
			})
		}
	}

	if len(mapping) > 0 {
		urlMap.DefaultService = mapping[0].PathMatcher[0].ServiceID
	}

	return urlMap
}
