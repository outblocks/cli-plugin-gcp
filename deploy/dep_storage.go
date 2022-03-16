package deploy

import (
	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
)

type StorageDep struct {
	Bucket *gcp.Bucket

	Dep   *apiv1.Dependency
	Opts  *StorageDepOptions
	Needs map[*apiv1.App]*StorageDepNeed
}

type StorageDepArgs struct {
	ProjectID string
	Region    string
	Needs     map[*apiv1.App]*StorageDepNeed
}

type StorageDepNeed struct{}

func NewStorageDepNeed(in map[string]interface{}) (*StorageDepNeed, error) {
	o := &StorageDepNeed{}
	return o, mapstructure.Decode(in, o)
}

func NewStorageDep(dep *apiv1.Dependency) (*StorageDep, error) {
	opts, err := NewStorageDepOptions(dep.Properties.AsMap(), dep.Type)
	if err != nil {
		return nil, err
	}

	return &StorageDep{
		Dep:  dep,
		Opts: opts,
	}, nil
}

type StorageDepOptions struct {
	Name       string `mapstructure:"name"`
	Versioning bool   `mapstructure:"versioning"`
	Location   string `mapstructure:"location"`
}

func NewStorageDepOptions(in map[string]interface{}, typ string) (*StorageDepOptions, error) {
	o := &StorageDepOptions{}

	err := mapstructure.Decode(in, o)
	if err != nil {
		return nil, err
	}

	return o, nil
}

func (o *StorageDep) Plan(pctx *config.PluginContext, r *registry.Registry, c *StorageDepArgs) error {
	o.Needs = c.Needs

	location := o.Opts.Location
	if location == "" {
		location = c.Region
	}

	// Add bucket.
	o.Bucket = &gcp.Bucket{
		Name:       fields.String(o.Opts.Name),
		Location:   fields.String(location),
		ProjectID:  fields.String(c.ProjectID),
		Versioning: fields.Bool(o.Opts.Versioning),
	}

	_, err := r.RegisterDependencyResource(o.Dep, "bucket", o.Bucket)
	if err != nil {
		return err
	}

	return nil
}
