package deploy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
	"google.golang.org/api/iterator"
)

type GCPBucket struct {
	Name       string               `json:"name"`
	ProjectID  string               `json:"project_id" mapstructure:"project_id"`
	Location   *string              `json:"location"`
	Versioning *bool                `json:"versioning"`
	IsPublic   *bool                `json:"is_public" mapstructure:"is_public"`
	Files      map[string]*FileInfo `json:"files"`
}

func (o *GCPBucket) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPBucket
		Type string `json:"type"`
	}{
		GCPBucket: *o,
		Type:      "gcp_bucket",
	})
}

type GCPBucketCreate struct {
	Name       string
	ProjectID  string
	Location   string
	Versioning bool
	IsPublic   bool
	Path       string
}

type GCPBucketPlan struct {
	Name        string               `json:"name"`
	ProjectID   string               `json:"project_id"`
	Path        string               `json:"path"`
	Location    *string              `json:"location,omitempty"`
	Versioning  *bool                `json:"versioning,omitempty"`
	IsPublic    *bool                `json:"is_public,omitempty"`
	FilesAdd    map[string]*FileInfo `json:"files_add,omitempty"`
	FilesUpdate map[string]*FileInfo `json:"files_update,omitempty"`
	FilesDelete map[string]*FileInfo `json:"files_delete,omitempty"`
}

func (o *GCPBucketPlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func (o *GCPBucket) verify(ctx context.Context, cli *storage.Client, name string) error {
	if name == "" {
		return nil
	}

	cur, err := cli.Bucket(name).Attrs(ctx)
	if err == storage.ErrBucketNotExist {
		o.Name = ""

		return nil
	} else if err != nil {
		return err
	}

	o.Name = name
	o.Location = &cur.Location
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

func deleteGCPBucketOperation(o *GCPBucket) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&GCPBucketPlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createGCPBucketPlan(c *GCPBucketCreate, files map[string]*FileInfo) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&GCPBucketPlan{
			Name:       c.Name,
			ProjectID:  c.ProjectID,
			Path:       c.Path,
			Location:   &c.Location,
			Versioning: &c.Versioning,
			IsPublic:   &c.IsPublic,
			FilesAdd:   files,
		}).Encode(),
	}
}

func (o *GCPBucket) Plan(ctx context.Context, cli *storage.Client, c *GCPBucketCreate, verify bool) (*types.PlanAction, error) {
	var ops []*types.PlanActionOperation

	// Fetch current state if needed.
	if verify {
		name := o.Name
		if name == "" && c != nil {
			name = c.Name
		}

		err := o.verify(ctx, cli, name)
		if err != nil {
			return nil, err
		}
	}

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(plugin_util.DeleteDesc("bucket", o.Name), append(ops, deleteGCPBucketOperation(o))), nil
		}

		return nil, nil
	}

	// Compute desired state.
	var files map[string]*FileInfo

	files, err := findFiles(c.Path)
	if err != nil {
		return nil, err
	}

	dest := &GCPBucket{
		Name:       c.Name,
		ProjectID:  c.ProjectID,
		Location:   &c.Location,
		Versioning: &c.Versioning,
		IsPublic:   &c.IsPublic,
		Files:      files,
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(plugin_util.AddDesc("bucket", c.Name, "%d file(s)", len(files)),
			append(ops, createGCPBucketPlan(c, files))), nil
	}

	// Check for conflicting updates.
	if !util.CompareIStringPtr(o.Location, dest.Location) {
		return types.NewPlanActionRecreate(plugin_util.UpdateDesc("bucket", o.Name, "forces recreate, %d file(s)", len(files)),
			append(ops, deleteGCPBucketOperation(o), createGCPBucketPlan(c, files))), nil
	}

	// Check for partial updates.
	steps := 0

	plan := &GCPBucketPlan{
		Name:      c.Name,
		ProjectID: c.ProjectID,
		Path:      c.Path,
	}

	if !util.CompareBoolPtr(o.Versioning, dest.Versioning) {
		plan.Versioning = dest.Versioning
		steps = 1
	}

	if !util.CompareBoolPtr(o.IsPublic, dest.IsPublic) {
		plan.IsPublic = dest.IsPublic
		steps = 1
	}

	var desc []string

	if steps != 0 {
		desc = append(desc, plugin_util.UpdateDesc("bucket", o.Name, "in-place"))
	}

	// File updates.
	addF, updateF, delF := diffFiles(o.Files, dest.Files)

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
		return types.NewPlanActionUpdate(strings.Join(desc, ", "),
			append(ops, &types.PlanActionOperation{Operation: types.PlanOpUpdate, Steps: steps, Data: plan.Encode()})), nil
	}

	return nil, nil
}

func decodeGCPBucketPlan(p *types.PlanActionOperation) (ret *GCPBucketPlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func applyGCPBucketFiles(ctx context.Context, b *storage.BucketHandle, cur map[string]*FileInfo, path string, add, upd, del map[string]*FileInfo, callback func(progress int)) error {
	var progress int32

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

			callback(int(atomic.AddInt32(&progress, 1)))

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

			callback(int(atomic.AddInt32(&progress, 1)))

			return nil
		})
	}

	return g.Wait()
}

func (o *GCPBucket) applyDeletePlan(ctx context.Context, cli *storage.Client, plan *GCPBucketPlan, action *types.ApplyAction, callback func(*types.ApplyAction)) (*types.ApplyAction, error) {
	bucket := cli.Bucket(plan.Name)

	iter := bucket.Objects(ctx, nil)

	var todel []string

	for {
		attrs, err := iter.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			return action, err
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
			return action, err
		}
	}

	if err := bucket.Delete(ctx); err != nil {
		return action, err
	}

	action = action.ProgressInc()
	callback(action)

	return action, nil
}

func (o *GCPBucket) applyUpdatePlan(ctx context.Context, cli *storage.Client, plan *GCPBucketPlan, action *types.ApplyAction, callback func(*types.ApplyAction)) (*types.ApplyAction, error) {
	bucket := cli.Bucket(plan.Name)

	update := false
	attrs := storage.BucketAttrsToUpdate{}

	if plan.Versioning != nil {
		attrs.VersioningEnabled = true
		update = true
	}

	if plan.IsPublic != nil {
		attrs.PredefinedACL = ACLPublicRead
		update = true
	}

	if update {
		if _, err := bucket.Update(ctx, attrs); err != nil {
			return action, err
		}

		action = action.ProgressInc()
		callback(action)
	}

	if plan.Versioning != nil {
		o.Versioning = plan.Versioning
	}

	if plan.IsPublic != nil {
		o.IsPublic = plan.IsPublic
	}

	// Apply files if needed.
	initial := action.Progress

	err := applyGCPBucketFiles(ctx, bucket, o.Files, plan.Path, plan.FilesAdd, plan.FilesUpdate, plan.FilesDelete, func(c int) {
		action.Progress = initial + c
		callback(action)
	})

	return action, err
}

func (o *GCPBucket) applyCreatePlan(ctx context.Context, cli *storage.Client, plan *GCPBucketPlan, action *types.ApplyAction, callback func(*types.ApplyAction)) (*types.ApplyAction, error) {
	bucket := cli.Bucket(plan.Name)

	attrs := &storage.BucketAttrs{Location: *plan.Location, VersioningEnabled: *plan.Versioning}

	if plan.IsPublic != nil && *plan.IsPublic {
		attrs.PredefinedACL = ACLPublicRead
	}

	if err := bucket.Create(ctx, plan.ProjectID, attrs); err != nil {
		return action, err
	}

	action = action.ProgressInc()
	callback(action)

	// Apply files if needed.
	initial := action.Progress

	o.Name = plan.Name
	o.ProjectID = plan.ProjectID
	o.Location = plan.Location
	o.Versioning = plan.Versioning
	o.IsPublic = plan.IsPublic

	err := applyGCPBucketFiles(ctx, bucket, o.Files, plan.Path, plan.FilesAdd, plan.FilesUpdate, plan.FilesDelete, func(c int) {
		action.Progress = initial + c
		callback(action)
	})

	return action, err
}

func (o *GCPBucket) Apply(ctx context.Context, cli *storage.Client, target, obj string, a *types.PlanAction, callback func(*types.ApplyAction)) error {
	if a == nil {
		return nil
	}

	if o.Files == nil {
		o.Files = make(map[string]*FileInfo)
	}

	// Calculate total.
	total := a.TotalSteps()
	if total == 0 {
		return nil
	}

	applyAction := &types.ApplyAction{
		Target:      target,
		Object:      obj,
		Description: a.Description,
		Progress:    0,
		Total:       total,
	}
	callback(applyAction)

	// Process operations.
	for _, p := range a.Operations {
		plan, err := decodeGCPBucketPlan(p)
		if err != nil {
			return err
		}

		switch p.Operation {
		case types.PlanOpDelete:
			// Deletion.
			applyAction, err = o.applyDeletePlan(ctx, cli, plan, applyAction, callback)
			if err != nil {
				return err
			}

		case types.PlanOpUpdate:
			// Updates.
			applyAction, err = o.applyUpdatePlan(ctx, cli, plan, applyAction, callback)
			if err != nil {
				return err
			}

		case types.PlanOpAdd:
			// Creation.
			applyAction, err = o.applyCreatePlan(ctx, cli, plan, applyAction, callback)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
