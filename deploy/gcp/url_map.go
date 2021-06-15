package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/compute/v1"
)

const URLMapName = "URL map"

type URLMap struct {
	Name      string        `json:"name"`
	ProjectID string        `json:"project_id" mapstructure:"project_id"`
	Mapping   []*URLMapping `json:"mapping"`
}

type URLMapping struct {
	Host        string            `json:"host"`
	PathMatcher []*URLPathMatcher `json:"path_matchers" mapstructure:"path_matchers"`
}

type URLPathMatcher struct {
	Paths     []string `json:"paths"`
	ServiceID string   `json:"service_id" mapstructure:"service_id"`
}

func (o *URLMap) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		URLMap
		Type string `json:"type"`
	}{
		URLMap: *o,
		Type:   "gcp_url_map",
	})
}

type URLMapCreate struct {
	Name            string
	ProjectID       string
	Mapping         []*URLMapping
	InvalidatePaths []string
}

func (o *URLMapCreate) ID() string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/urlMaps/%s", o.ProjectID, o.Name)
}

type URLMapPlan URLMapCreate

func (o *URLMapPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func CleanupURLMapping(m []*URLMapping) []*URLMapping {
	hmap := make(map[string][]*URLPathMatcher)

	for _, u := range m {
		hmap[u.Host] = append(hmap[u.Host], u.PathMatcher...)
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

	return ret
}

func splitURL(url string) (host, path string) {
	urlSplit := strings.SplitN(url, "/", 2)

	if len(urlSplit) == 2 {
		path = urlSplit[1]
	}

	if path == "/" || path == "" {
		path = "/*"
	}

	return urlSplit[0], path
}

func CreateURLMapping(url, serviceID string) *URLMapping {
	host, path := splitURL(url)

	ret := &URLMapping{
		Host: host,
		PathMatcher: []*URLPathMatcher{
			{
				Paths:     []string{path},
				ServiceID: serviceID,
			},
		},
	}

	return ret
}

func makeURLMap(name, projectID string, matchers []*URLMapping) *compute.UrlMap {
	urlMap := &compute.UrlMap{
		Name: name,
	}

	for _, matcher := range matchers {
		host := matcher.Host
		urlMap.DefaultService = matcher.PathMatcher[0].ServiceID
		urlMap.HostRules = append(urlMap.HostRules, &compute.HostRule{
			Hosts:       []string{host},
			PathMatcher: ID(name, projectID, host),
		})

		pathMatcher := &compute.PathMatcher{
			Name:           ID(name, projectID, host),
			DefaultService: matcher.PathMatcher[0].ServiceID,
		}

		urlMap.PathMatchers = append(urlMap.PathMatchers, pathMatcher)

		for _, m := range matcher.PathMatcher {
			pathMatcher.PathRules = append(pathMatcher.PathRules, &compute.PathRule{
				Paths:   m.Paths,
				Service: m.ServiceID,
			})
		}
	}

	return urlMap
}

func (o *URLMap) verify(cli *compute.Service, c *URLMapCreate) error {
	name := o.Name
	projectID := o.ProjectID

	if name == "" && c != nil {
		name = c.Name
		projectID = c.ProjectID
	}

	if name == "" {
		return nil
	}

	_, err := cli.UrlMaps.Get(projectID, name).Do()
	if ErrIs404(err) {
		*o = URLMap{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.ProjectID = projectID

	return nil
}

func (o *URLMap) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *URLMapCreate
	)

	if dest != nil {
		c = dest.(*URLMapCreate)
	}

	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return nil, err
	}

	// Fetch current state if needed.
	if verify {
		err := o.verify(cli, c)
		if err != nil {
			return nil, err
		}
	}

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(URLMapName, o.Name),
				append(ops, deleteURLMapOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(URLMapName, c.Name),
			append(ops, createURLMapOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(URLMapName, c.Name, "forces recreate"),
			append(ops, deleteURLMapOp(o), createURLMapOp(c))), nil
	}

	// Check for partial updates.
	if !reflect.DeepEqual(o.Mapping, c.Mapping) || len(c.InvalidatePaths) != 0 {
		plan := &URLMapPlan{
			Name:            o.Name,
			ProjectID:       o.ProjectID,
			Mapping:         c.Mapping,
			InvalidatePaths: c.InvalidatePaths,
		}

		return types.NewPlanActionUpdate(key, plugin_util.UpdateDesc(URLMapName, c.Name, "in-place"),
			append(ops, &types.PlanActionOperation{Operation: types.PlanOpUpdate, Steps: 1, Data: plan.Encode()})), nil
	}

	return nil, nil
}

func deleteURLMapOp(o *URLMap) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&URLMapPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createURLMapOp(c *URLMapCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&URLMapPlan{
			Name:            c.Name,
			ProjectID:       c.ProjectID,
			Mapping:         c.Mapping,
			InvalidatePaths: c.InvalidatePaths,
		}).Encode(),
	}
}

func decodeURLMapPlan(p *types.PlanActionOperation) (ret *URLMapPlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func (o *URLMap) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.GCPComputeClient()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeURLMapPlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			oper, err := cli.UrlMaps.Delete(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(URLMapName, plan.Name))

		case types.PlanOpAdd:
			// Creation.
			oper, err := cli.UrlMaps.Insert(plan.ProjectID, makeURLMap(plan.Name, plan.ProjectID, plan.Mapping)).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(URLMapName, plan.Name))

			_, err = cli.UrlMaps.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.Name = plan.Name
			o.ProjectID = plan.ProjectID
			o.Mapping = plan.Mapping

		case types.PlanOpUpdate:
			oper, err := cli.UrlMaps.Patch(plan.ProjectID, plan.Name, makeURLMap(plan.Name, plan.ProjectID, plan.Mapping)).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(URLMapName, plan.Name))

			_, err = cli.UrlMaps.Get(plan.ProjectID, plan.Name).Do()
			if err != nil {
				return err
			}

			o.Mapping = plan.Mapping
		}

		// Invalidate cache.
		for _, p := range plan.InvalidatePaths {
			host, path := splitURL(p)

			oper, err := cli.UrlMaps.InvalidateCache(plan.ProjectID, plan.Name, &compute.CacheInvalidationRule{
				Host: host,
				Path: path,
			}).Do()
			if err != nil {
				return err
			}

			err = waitForGlobalOperation(cli, plan.ProjectID, oper.Name)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
