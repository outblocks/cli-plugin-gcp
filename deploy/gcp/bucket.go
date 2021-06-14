package gcp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
	"google.golang.org/api/iterator"
)

const BucketName = "bucket"

type Bucket struct {
	Name       string               `json:"name"`
	ProjectID  string               `json:"project_id" mapstructure:"project_id"`
	Location   string               `json:"location"`
	Versioning *bool                `json:"versioning"`
	IsPublic   *bool                `json:"is_public" mapstructure:"is_public"`
	Files      map[string]*FileInfo `json:"files"`
}

func (o *Bucket) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Bucket
		Type string `json:"type"`
	}{
		Bucket: *o,
		Type:   "gcp_bucket",
	})
}

type BucketCreate struct {
	Name       string
	ProjectID  string
	Location   string
	Versioning bool
	IsPublic   bool
	Path       string
}

type BucketPlan struct {
	Name        string               `json:"name"`
	ProjectID   string               `json:"project_id"`
	Path        string               `json:"path"`
	Location    string               `json:"location,omitempty"`
	Versioning  *bool                `json:"versioning,omitempty"`
	IsPublic    *bool                `json:"is_public,omitempty"`
	FilesAdd    map[string]*FileInfo `json:"files_add,omitempty"`
	FilesUpdate map[string]*FileInfo `json:"files_update,omitempty"`
	FilesDelete map[string]*FileInfo `json:"files_delete,omitempty"`
}

func (o *BucketPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func (o *Bucket) verify(ctx context.Context, cli *storage.Client, c *BucketCreate) error {
	name := o.Name
	if name == "" && c != nil {
		name = c.Name
	}

	if name == "" {
		return nil
	}

	cur, err := cli.Bucket(name).Attrs(ctx)
	if err == storage.ErrBucketNotExist {
		*o = Bucket{}

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.Location = cur.Location
	o.Versioning = &cur.VersioningEnabled
	o.Files = make(map[string]*FileInfo)

	iter := cli.Bucket(name).Objects(ctx, nil)

	var attrs *storage.ObjectAttrs

	for {
		attrs, err = iter.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			return err
		}

		o.Files[attrs.Name] = &FileInfo{
			Hash: hex.EncodeToString(attrs.MD5),
		}
	}

	return nil
}

func deleteBucketOp(o *Bucket) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&BucketPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createBucketOp(c *BucketCreate, files map[string]*FileInfo) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1 + len(files),
		Operation: types.PlanOpAdd,
		Data: (&BucketPlan{
			Name:       c.Name,
			ProjectID:  c.ProjectID,
			Path:       c.Path,
			Location:   c.Location,
			Versioning: &c.Versioning,
			IsPublic:   &c.IsPublic,
			FilesAdd:   files,
		}).Encode(),
	}
}

func (o *Bucket) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *BucketCreate
	)

	if dest != nil {
		c = dest.(*BucketCreate)
	}

	pctx := ctx.(*config.PluginContext)

	cli, err := pctx.StorageClient()
	if err != nil {
		return nil, err
	}

	// Fetch current state if needed.
	if verify {
		err := o.verify(pctx, cli, c)
		if err != nil {
			return nil, err
		}
	}

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(BucketName, o.Name), append(ops, deleteBucketOp(o))), nil
		}

		return nil, nil
	}

	// Compute desired state.
	files, err := findFiles(c.Path)
	if err != nil {
		return nil, err
	}

	wants := &Bucket{
		Name:       c.Name,
		ProjectID:  c.ProjectID,
		Location:   c.Location,
		Versioning: &c.Versioning,
		IsPublic:   &c.IsPublic,
		Files:      files,
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(BucketName, c.Name, "%d file(s)", len(files)),
			append(ops, createBucketOp(c, files))), nil
	}

	// Check for conflicting updates.
	if !strings.EqualFold(o.Location, wants.Location) {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(BucketName, o.Name, "forces recreate, %d file(s)", len(files)),
			append(ops, deleteBucketOp(o), createBucketOp(c, files))), nil
	}

	// Check for partial updates.
	steps := 0

	plan := &BucketPlan{
		Name:      c.Name,
		ProjectID: c.ProjectID,
		Path:      c.Path,
	}

	if !util.CompareBoolPtr(o.Versioning, wants.Versioning) {
		plan.Versioning = wants.Versioning
		steps = 1
	}

	if !util.CompareBoolPtr(o.IsPublic, wants.IsPublic) {
		plan.IsPublic = wants.IsPublic
		steps = 1
	}

	var desc []string

	if steps != 0 {
		desc = append(desc, plugin_util.UpdateDesc(BucketName, o.Name, "in-place"))
	}

	// File updates.
	addF, updateF, delF := diffFiles(o.Files, wants.Files)

	if len(addF) != 0 {
		plan.FilesAdd = addF
		steps += len(addF)

		desc = append(desc, plugin_util.AddDesc("files to bucket", c.Name, "%d file(s)", len(addF)))
	}

	if len(updateF) != 0 {
		plan.FilesUpdate = updateF
		steps += len(updateF)

		desc = append(desc, plugin_util.UpdateDesc("files in bucket", c.Name, "%d file(s)", len(updateF)))
	}

	if len(delF) != 0 {
		plan.FilesDelete = delF
		steps += len(delF)

		desc = append(desc, plugin_util.DeleteDesc("files from bucket", c.Name, "%d file(s)", len(delF)))
	}

	if steps > 0 {
		return types.NewPlanActionUpdate(key, strings.Join(desc, ", "),
			append(ops, &types.PlanActionOperation{Operation: types.PlanOpUpdate, Steps: steps, Data: plan.Encode()})), nil
	}

	return nil, nil
}

func decodeBucketPlan(p *types.PlanActionOperation) (ret *BucketPlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func applyBucketFiles(ctx context.Context, b *storage.BucketHandle, cur map[string]*FileInfo, path string, add, upd, del map[string]*FileInfo, callback func(f string)) error {
	for k, v := range upd {
		add[k] = v
	}

	g, _ := errgroup.WithConcurrency(ctx, defaultConcurrency)

	for name, f := range add {
		name := name
		f := f

		g.Go(func() error {
			file, err := os.Open(filepath.Join(path, name))
			if err != nil {
				return err
			}

			w := b.Object(name).NewWriter(ctx)
			_, err = io.Copy(w, file)
			_ = file.Close()

			if err != nil {
				return err
			}

			err = w.Close()
			if err != nil {
				return err
			}

			cur[name] = f

			callback(name)

			return nil
		})
	}

	for name := range del {
		name := name

		g.Go(func() error {
			err := b.Object(name).Delete(ctx)
			if err != nil {
				return err
			}

			delete(cur, name)

			callback(name)

			return nil
		})
	}

	return g.Wait()
}

func (o *Bucket) applyDeletePlan(ctx context.Context, cli *storage.Client, plan *BucketPlan) error {
	bucket := cli.Bucket(plan.Name)

	iter := bucket.Objects(ctx, nil)

	var todel []string

	for {
		attrs, err := iter.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			return err
		}

		todel = append(todel, attrs.Name)
	}

	if len(todel) != 0 {
		g, _ := errgroup.WithConcurrency(ctx, defaultConcurrency)

		for _, n := range todel {
			n := n

			g.Go(func() error {
				return bucket.Object(n).Delete(ctx)
			})
		}

		err := g.Wait()
		if err != nil {
			return err
		}
	}

	if err := bucket.Delete(ctx); err != nil {
		return err
	}

	return nil
}

func (o *Bucket) applyUpdatePlan(ctx context.Context, cli *storage.Client, plan *BucketPlan, callback func(op string)) error {
	bucket := cli.Bucket(plan.Name)

	update := false
	attrs := storage.BucketAttrsToUpdate{}

	if plan.Versioning != nil {
		attrs.VersioningEnabled = *plan.Versioning
		update = true
	}

	if plan.IsPublic != nil {
		if *plan.IsPublic {
			attrs.PredefinedACL = ACLPublicRead
		} else {
			attrs.PredefinedACL = ACLProjectPrivate
		}

		update = true
	}

	if update {
		if _, err := bucket.Update(ctx, attrs); err != nil {
			return err
		}

		callback(plugin_util.UpdateDesc(BucketName, o.Name, "in-place"))
	}

	if plan.Versioning != nil {
		o.Versioning = plan.Versioning
	}

	if plan.IsPublic != nil {
		o.IsPublic = plan.IsPublic
	}

	// Apply files if needed.
	err := applyBucketFiles(ctx, bucket, o.Files, plan.Path, plan.FilesAdd, plan.FilesUpdate, plan.FilesDelete, func(f string) {
		callback(plugin_util.UpdateDesc("files in bucket", o.Name, fmt.Sprintf("file: %s", f)))
	})

	return err
}

func (o *Bucket) applyCreatePlan(ctx context.Context, cli *storage.Client, plan *BucketPlan, callback func(op string)) error {
	bucket := cli.Bucket(plan.Name)

	attrs := &storage.BucketAttrs{Location: plan.Location, VersioningEnabled: *plan.Versioning}

	if plan.IsPublic != nil && *plan.IsPublic {
		attrs.PredefinedACL = ACLPublicRead
	}

	if err := bucket.Create(ctx, plan.ProjectID, attrs); err != nil {
		return err
	}

	o.Name = plan.Name
	o.ProjectID = plan.ProjectID
	o.Location = plan.Location
	o.Versioning = plan.Versioning
	o.IsPublic = plan.IsPublic

	callback(plugin_util.AddDesc(BucketName, o.Name))

	// Apply files if needed.

	err := applyBucketFiles(ctx, bucket, o.Files, plan.Path, plan.FilesAdd, plan.FilesUpdate, plan.FilesDelete, func(f string) {
		callback(plugin_util.UpdateDesc("files in bucket", o.Name, f))
	})

	return err
}

func (o *Bucket) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	if o.Files == nil {
		o.Files = make(map[string]*FileInfo)
	}

	cli, err := pctx.StorageClient()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeBucketPlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			err = o.applyDeletePlan(pctx, cli, plan)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(BucketName, o.Name))

		case types.PlanOpUpdate:
			// Updates.
			err = o.applyUpdatePlan(pctx, cli, plan, callback)
			if err != nil {
				return err
			}

		case types.PlanOpAdd:
			// Creation.
			err = o.applyCreatePlan(pctx, cli, plan, callback)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
