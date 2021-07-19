package gcp

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/iterator"
)

type BucketObject struct {
	registry.ResourceBase

	BucketName fields.StringInputField `state:"force_new"`
	Name       fields.StringInputField `state:"force_new"`
	Hash       fields.StringInputField
	IsPublic   fields.BoolInputField

	Path string `state:"-"`
}

func (o *BucketObject) GetName() string {
	return o.Name.Any()
}

func (o *BucketObject) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	bucket := o.BucketName.Any()

	files, err := pctx.FuncCache(fmt.Sprintf("BucketObject:list:%s", bucket), func() (interface{}, error) {
		iter := cli.Bucket(bucket).Objects(ctx, nil)

		ret := make(map[string]*storage.ObjectAttrs)

		for {
			attrs, err := iter.Next()
			if err == iterator.Done {
				break
			}

			if err != nil {
				return nil, err
			}

			ret[attrs.Name] = attrs
		}

		return ret, nil
	})
	if err == storage.ErrBucketNotExist {
		o.MarkAsNew()

		return nil
	}

	if err != nil {
		return fmt.Errorf("error fetching bucket object status: %w", err)
	}

	attrs, ok := files.(map[string]*storage.ObjectAttrs)[o.Name.Any()]
	if !ok {
		o.MarkAsNew()

		return nil
	}

	isPublic := false

	for _, acl := range attrs.ACL {
		if acl.Entity == storage.AllUsers && acl.Role == storage.RoleReader {
			isPublic = true
		}
	}

	o.MarkAsExisting()
	o.BucketName.SetCurrent(attrs.Bucket)
	o.Name.SetCurrent(attrs.Name)
	o.Hash.SetCurrent(hex.EncodeToString(attrs.MD5))
	o.IsPublic.SetCurrent(isPublic)

	return nil
}

func (o *BucketObject) uploadFile(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	file, err := os.Open(o.Path)
	if err != nil {
		return err
	}

	w := cli.Bucket(o.BucketName.Wanted()).Object(o.Name.Wanted()).NewWriter(ctx)
	w.ACL = []storage.ACLRule{{Entity: storage.AllUsers, Role: storage.RoleReader}}
	_, err = io.Copy(w, file)
	_ = file.Close()

	if err != nil {
		return err
	}

	return w.Close()
}

func (o *BucketObject) Create(ctx context.Context, meta interface{}) error {
	return o.uploadFile(ctx, meta)
}

func (o *BucketObject) Update(ctx context.Context, meta interface{}) error {
	return o.uploadFile(ctx, meta)
}

func (o *BucketObject) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	return cli.Bucket(o.BucketName.Current()).Object(o.Name.Current()).Delete(ctx)
}
