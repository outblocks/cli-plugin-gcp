package deploy

import (
	"time"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
)

type StorageDep struct {
	Bucket *gcp.Bucket

	Dep   *apiv1.Dependency
	Opts  *types.StorageDepOptions
	Needs map[*apiv1.App]*StorageDepNeed
}

type StorageDepArgs struct {
	ProjectID string
	Region    string
	Needs     map[*apiv1.App]*StorageDepNeed
}

type StorageDepNeed struct{}

func NewStorageDepNeed(in map[string]any) (*StorageDepNeed, error) {
	o := &StorageDepNeed{}

	return o, plugin_util.MapstructureJSONDecode(in, o)
}

func NewStorageDep(dep *apiv1.Dependency) (*StorageDep, error) {
	opts, err := types.NewStorageDepOptions(dep.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	return &StorageDep{
		Dep:  dep,
		Opts: opts,
	}, nil
}

func (o *StorageDep) Plan(_ *config.PluginContext, r *registry.Registry, c *StorageDepArgs) error {
	o.Needs = c.Needs

	location := o.Opts.Location
	if location == "" {
		location = c.Region
	}

	cors := make([]storage.CORS, len(o.Opts.CORS))
	for i, v := range o.Opts.CORS {
		cors[i] = storage.CORS{
			Origins:         v.Origins,
			Methods:         v.Methods,
			ResponseHeaders: v.ResponseHeaders,
			MaxAge:          time.Duration(v.MaxAgeInSeconds) * time.Second,
		}
	}

	// Add bucket.
	o.Bucket = &gcp.Bucket{
		Name:                 fields.String(o.Opts.Name),
		Location:             fields.String(location),
		ProjectID:            fields.String(c.ProjectID),
		Versioning:           fields.Bool(o.Opts.Versioning),
		DeleteInDays:         fields.Int(o.Opts.DeleteInDays),
		ExpireVersionsInDays: fields.Int(o.Opts.ExpireVersionsDays),
		MaxVersions:          fields.Int(o.Opts.MaxVersions),
		Public:               fields.Bool(o.Opts.Public),
		CORS:                 gcp.BucketCORS(cors),
		Critical:             true,
	}

	_, err := r.RegisterDependencyResource(o.Dep, "bucket", o.Bucket)
	if err != nil {
		return err
	}

	return nil
}
