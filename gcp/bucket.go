package gcp

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
	"google.golang.org/api/iterator"
)

type Bucket struct {
	registry.ResourceBase

	Name       fields.StringInputField `state:"force_new"`
	Location   fields.StringInputField `state:"force_new"`
	ProjectID  fields.StringInputField
	Versioning fields.BoolInputField
}

func (o *Bucket) UniqueID() string {
	return o.Name.Any()
}

func (o *Bucket) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *Bucket) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	attrs, err := cli.Bucket(o.Name.Any()).Attrs(ctx)
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

	return nil
}

func (o *Bucket) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	attrs := &storage.BucketAttrs{Location: o.Location.Wanted(), VersioningEnabled: o.Versioning.Wanted()}

	return cli.Bucket(o.Name.Wanted()).Create(ctx, o.ProjectID.Wanted(), attrs)
}

func (o *Bucket) Update(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	attrs := storage.BucketAttrsToUpdate{VersioningEnabled: o.Versioning.Wanted()}

	_, err = cli.Bucket(o.Name.Wanted()).Update(ctx, attrs)

	return err
}

func (o *Bucket) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.StorageClient(ctx)
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
