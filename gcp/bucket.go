package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
	"google.golang.org/api/iterator"
)

func BucketCORS(in []storage.CORS) fields.ArrayInputField {
	ret := make([]fields.Field, len(in))

	for i, v := range in {
		out, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}

		ret[i] = fields.String(string(out))
	}

	return fields.Array(ret)
}

func bucketCORSFromInterface(arr []interface{}) []storage.CORS {
	ret := make([]storage.CORS, 0, len(arr))

	for _, v := range arr {
		o := &storage.CORS{}

		err := json.Unmarshal([]byte(v.(string)), o)
		if err != nil {
			continue
		}

		ret = append(ret, *o)
	}

	return ret
}

type Bucket struct {
	registry.ResourceBase

	Name                 fields.StringInputField `state:"force_new"`
	Location             fields.StringInputField `state:"force_new"`
	ProjectID            fields.StringInputField
	Versioning           fields.BoolInputField
	DeleteInDays         fields.IntInputField
	ExpireVersionsInDays fields.IntInputField
	MaxVersions          fields.IntInputField
	Public               fields.BoolInputField

	CORS fields.ArrayInputField
}

func (o *Bucket) ReferenceID() string {
	return o.Name.Any()
}

func (o *Bucket) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *Bucket) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPStorageClient(ctx)
	if err != nil {
		return err
	}

	b := cli.Bucket(o.Name.Any())

	attrs, err := b.Attrs(ctx)
	if err == storage.ErrBucketNotExist {
		o.MarkAsNew()

		return nil
	}

	if err != nil {
		return fmt.Errorf("error fetching bucket status: %w", err)
	}

	o.MarkAsExisting()
	o.Name.SetCurrent(attrs.Name)
	o.Location.SetCurrent(strings.ToLower(attrs.Location))
	o.Versioning.SetCurrent(attrs.VersioningEnabled)

	// Cannot check project ID, assume that it is in correct project ID always.
	o.ProjectID.SetCurrent(o.ProjectID.Wanted())

	o.CORS.SetCurrent(BucketCORS(attrs.CORS).Wanted())

	policy, err := b.IAM().Policy(ctx)
	if err != nil {
		return fmt.Errorf("error fetching bucket policy: %w", err)
	}

	o.Public.SetCurrent(policy.HasRole(ACLAllUsers, "roles/storage.objectViewer"))

	return nil
}

func (o *Bucket) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPStorageClient(ctx)
	if err != nil {
		return err
	}

	b := cli.Bucket(o.Name.Wanted())
	attrs := &storage.BucketAttrs{
		Location:          o.Location.Wanted(),
		VersioningEnabled: o.Versioning.Wanted(),
		CORS:              bucketCORSFromInterface(o.CORS.Wanted()),
	}

	if o.ExpireVersionsInDays.Wanted() > 0 {
		attrs.Lifecycle.Rules = append(attrs.Lifecycle.Rules, storage.LifecycleRule{
			Condition: storage.LifecycleCondition{
				DaysSinceNoncurrentTime: int64(o.ExpireVersionsInDays.Wanted()),
			},
			Action: storage.LifecycleAction{
				Type: "Delete",
			},
		})
	}

	if o.DeleteInDays.Wanted() > 0 {
		attrs.Lifecycle.Rules = append(attrs.Lifecycle.Rules, storage.LifecycleRule{
			Condition: storage.LifecycleCondition{
				AgeInDays: int64(o.DeleteInDays.Wanted()),
			},
			Action: storage.LifecycleAction{
				Type: "Delete",
			},
		})
	}

	if o.MaxVersions.Wanted() > 0 {
		attrs.Lifecycle.Rules = append(attrs.Lifecycle.Rules, storage.LifecycleRule{
			Condition: storage.LifecycleCondition{
				NumNewerVersions: int64(o.MaxVersions.Current()),
				Liveness:         storage.Archived,
			},
			Action: storage.LifecycleAction{
				Type: "Delete",
			},
		})
	}

	err = b.Create(ctx, o.ProjectID.Wanted(), attrs)
	if err != nil {
		return fmt.Errorf("error creating bucket: %w", err)
	}

	if !o.Public.Wanted() {
		return nil
	}

	policy, err := b.IAM().Policy(ctx)
	if err != nil {
		return fmt.Errorf("error fetching bucket policy: %w", err)
	}

	policy.Add(ACLAllUsers, "roles/storage.objectViewer")

	err = b.IAM().SetPolicy(ctx, policy)
	if err != nil {
		return fmt.Errorf("error setting bucket policy: %w", err)
	}

	return nil
}

func (o *Bucket) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPStorageClient(ctx)
	if err != nil {
		return err
	}

	b := cli.Bucket(o.Name.Wanted())
	attrs := storage.BucketAttrsToUpdate{
		VersioningEnabled: o.Versioning.Wanted(),
		CORS:              bucketCORSFromInterface(o.CORS.Wanted()),
	}

	var lifecycle storage.Lifecycle

	if o.ExpireVersionsInDays.Wanted() > 0 {
		lifecycle.Rules = append(lifecycle.Rules, storage.LifecycleRule{
			Condition: storage.LifecycleCondition{
				DaysSinceNoncurrentTime: int64(o.ExpireVersionsInDays.Wanted()),
			},
			Action: storage.LifecycleAction{
				Type: "Delete",
			},
		})
	}

	if o.DeleteInDays.Wanted() > 0 {
		lifecycle.Rules = append(lifecycle.Rules, storage.LifecycleRule{
			Condition: storage.LifecycleCondition{
				AgeInDays: int64(o.DeleteInDays.Wanted()),
			},
			Action: storage.LifecycleAction{
				Type: "Delete",
			},
		})
	}

	if o.MaxVersions.Wanted() > 0 {
		lifecycle.Rules = append(lifecycle.Rules, storage.LifecycleRule{
			Condition: storage.LifecycleCondition{
				NumNewerVersions: int64(o.MaxVersions.Current()),
				Liveness:         storage.Archived,
			},
			Action: storage.LifecycleAction{
				Type: "Delete",
			},
		})
	}

	if len(lifecycle.Rules) > 0 {
		attrs.Lifecycle = &lifecycle
	}

	_, err = b.Update(ctx, attrs)
	if err != nil {
		return fmt.Errorf("error updating bucket: %w", err)
	}

	if !o.Public.IsChanged() {
		return nil
	}

	policy, err := b.IAM().Policy(ctx)
	if err != nil {
		return fmt.Errorf("error fetching bucket policy: %w", err)
	}

	if o.Public.Wanted() {
		policy.Add(ACLAllUsers, "roles/storage.objectViewer")
	} else {
		policy.Remove(ACLAllUsers, "roles/storage.objectViewer")
	}

	err = b.IAM().SetPolicy(ctx, policy)
	if err != nil {
		return fmt.Errorf("error setting bucket policy: %w", err)
	}

	return err
}

func (o *Bucket) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPStorageClient(ctx)
	if err != nil {
		return err
	}

	b := cli.Bucket(o.Name.Current())

	// Delete all objects.
	var todel []*storage.ObjectAttrs

	iter := b.Objects(ctx, &storage.Query{Versions: true})

	for {
		attrs, err := iter.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			return err
		}

		todel = append(todel, attrs)
	}

	g, _ := errgroup.WithConcurrency(ctx, DefaultConcurrency)

	for _, d := range todel {
		d := d

		g.Go(func() error {
			return b.Object(d.Name).Generation(d.Generation).Delete(ctx)
		})
	}

	err = g.Wait()
	if err != nil {
		return err
	}

	return b.Delete(ctx)
}

func (o *Bucket) IsCritical(t registry.DiffType, fieldList []string) bool {
	return t == registry.DiffTypeDelete || t == registry.DiffTypeRecreate
}
