package deploy

import (
	"time"

	"cloud.google.com/go/storage"
	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
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

	return o, plugin_util.MapstructureJSONDecode(in, o)
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
	Name               string `json:"name"`
	Versioning         bool   `json:"versioning"`
	Location           string `json:"location"`
	DeleteInDays       int    `json:"delete_in_days"`
	ExpireVersionsDays int    `json:"expire_versions_in_days"`
	MaxVersions        int    `json:"max_versions"`
	Public             bool   `json:"public"`

	CORS []struct {
		Origins         []string `json:"origin"`
		Methods         []string `json:"method"`
		ResponseHeaders []string `json:"response_header"`
		MaxAgeInSeconds int      `json:"max_age_in_seconds"`
	} `json:"cors"`
}

func NewStorageDepOptions(in map[string]interface{}, typ string) (*StorageDepOptions, error) {
	o := &StorageDepOptions{}

	err := mapstructure.WeakDecode(in, o)
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
	}

	_, err := r.RegisterDependencyResource(o.Dep, "bucket", o.Bucket)
	if err != nil {
		return err
	}

	return nil
}
