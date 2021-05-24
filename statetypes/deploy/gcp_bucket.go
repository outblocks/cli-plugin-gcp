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

func (b *GCPBucket) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPBucket
		Type string `json:"type"`
	}{
		GCPBucket: *b,
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

func (b *GCPBucketPlan) Encode() []byte {
	d, err := json.Marshal(b)
	if err != nil {
		panic(err)
	}

	return d
}

func (b *GCPBucket) Fetch(ctx context.Context, cli *storage.Client, name string) error {
	if name == "" {
		return nil
	}

	cur, err := cli.Bucket(name).Attrs(ctx)
	if err == storage.ErrBucketNotExist {
		b.Name = ""
	} else if err != nil {
		return err
	}

	b.Name = name
	b.Location = &cur.Location
	b.Versioning = &cur.VersioningEnabled
	b.Files = make(map[string]*FileInfo)

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

		b.Files[attrs.Name] = &FileInfo{
			Hash: hex.EncodeToString(attrs.MD5),
		}
	}

	return nil
}

func deleteGCPBucketOperation(b *GCPBucket) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Operation: types.PlanDelete,
		Data: (&GCPBucketPlan{
			Name:      b.Name,
			ProjectID: b.ProjectID,
		}).Encode(),
	}
}

func createGCPBucketPlan(c *GCPBucketCreate, files map[string]*FileInfo) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Operation: types.PlanAdd,
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

func (b *GCPBucket) Plan(ctx context.Context, cli *storage.Client, c *GCPBucketCreate, verify bool) (action *types.PlanAction, err error) {
	action = &types.PlanAction{}

	// Fetch current state if needed.
	if verify {
		name := b.Name
		if name == "" && c != nil {
			name = c.Name
		}

		err = b.Fetch(ctx, cli, name)
		if err != nil {
			return
		}
	}

	if c == nil {
		if b.Name != "" {
			action.Description = plugin_util.DeleteDesc("bucket", b.Name)
			action.Operations = append(action.Operations, deleteGCPBucketOperation(b))

			return
		}
	}

	// Compute desired state.
	var files map[string]*FileInfo

	files, err = findFiles(c.Path)
	if err != nil {
		return
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
	if b.Name == "" {
		action.Description = plugin_util.AddDesc("bucket", c.Name, "%d file(s)", len(files))
		action.Operations = append(action.Operations, createGCPBucketPlan(c, files))

		return
	}

	// Check for conflicting updates.
	if !util.CompareIStringPtr(b.Location, dest.Location) {
		action.Description = plugin_util.UpdateDesc("bucket", b.Name, "forces recreate, %d file(s)", len(files))
		action.Operations = append(action.Operations, deleteGCPBucketOperation(b), createGCPBucketPlan(c, files))

		return
	}

	// Check for partial updates.
	update := false

	plan := &GCPBucketPlan{
		Name:      c.Name,
		ProjectID: c.ProjectID,
		Path:      c.Path,
	}

	if !util.CompareBoolPtr(b.Versioning, dest.Versioning) {
		plan.Versioning = dest.Versioning
		update = true
	}

	if !util.CompareBoolPtr(b.IsPublic, dest.IsPublic) {
		plan.IsPublic = dest.IsPublic
		update = true
	}

	var desc []string

	if update {
		desc = append(desc, plugin_util.UpdateDesc("bucket", b.Name, "in-place"))
	}

	// File updates.
	addF, updateF, delF := diffFiles(b.Files, dest.Files)

	if len(addF) != 0 {
		plan.FilesAdd = addF
		update = true

		desc = append(desc, plugin_util.AddDesc("files to bucket", c.Name, "%d file(s)", len(updateF)))
	}

	if len(updateF) != 0 {
		plan.FilesUpdate = updateF
		update = true

		desc = append(desc, plugin_util.UpdateDesc("files in bucket", c.Name, "%d file(s)", len(updateF)))
	}

	if len(delF) != 0 {
		plan.FilesDelete = delF
		update = true

		desc = append(desc, plugin_util.DeleteDesc("files from bucket", c.Name, "%d file(s)", len(updateF)))
	}

	if update {
		action.Description = strings.Join(desc, ", ")

		action.Operations = append(action.Operations, &types.PlanActionOperation{
			Data: plan.Encode(),
		})
	}

	return action, nil
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

func (b *GCPBucket) applyDeletePlan(ctx context.Context, cli *storage.Client, plan *GCPBucketPlan, action *types.ApplyAction, callback func(*types.ApplyAction)) (*types.ApplyAction, error) {
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

func (b *GCPBucket) applyUpdatePlan(ctx context.Context, cli *storage.Client, plan *GCPBucketPlan, action *types.ApplyAction, callback func(*types.ApplyAction)) (*types.ApplyAction, error) {
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
		b.Versioning = plan.Versioning
	}

	if plan.IsPublic != nil {
		b.IsPublic = plan.IsPublic
	}

	// Apply files if needed.
	initial := action.Progress

	err := applyGCPBucketFiles(ctx, bucket, b.Files, plan.Path, plan.FilesAdd, plan.FilesUpdate, plan.FilesDelete, func(c int) {
		action.Progress = initial + c
		callback(action)
	})

	return action, err
}

func (b *GCPBucket) applyCreatePlan(ctx context.Context, cli *storage.Client, plan *GCPBucketPlan, action *types.ApplyAction, callback func(*types.ApplyAction)) (*types.ApplyAction, error) {
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

	b.Name = plan.Name
	b.ProjectID = plan.ProjectID
	b.Location = plan.Location
	b.Versioning = plan.Versioning
	b.IsPublic = plan.IsPublic

	err := applyGCPBucketFiles(ctx, bucket, b.Files, plan.Path, plan.FilesAdd, plan.FilesUpdate, plan.FilesDelete, func(c int) {
		action.Progress = initial + c
		callback(action)
	})

	return action, err
}

func (b *GCPBucket) Apply(ctx context.Context, cli *storage.Client, obj string, a *types.PlanAction, callback func(*types.ApplyAction)) error {
	if a == nil {
		return nil
	}

	if b.Files == nil {
		b.Files = make(map[string]*FileInfo)
	}

	planMap := make(map[*types.PlanActionOperation]*GCPBucketPlan)

	// Calculate total.
	total := 0

	for _, p := range a.Operations {
		plan, err := decodeGCPBucketPlan(p)
		if err != nil {
			return err
		}

		planMap[p] = plan

		switch p.Operation {
		case types.PlanDelete:
			total++
		case types.PlanUpdate:
			total += len(plan.FilesAdd) + len(plan.FilesDelete) + len(plan.FilesUpdate)
			if plan.Versioning != nil || plan.IsPublic != nil {
				total++
			}
		case types.PlanAdd:
			total += 1 + len(plan.FilesAdd) + len(plan.FilesDelete) + len(plan.FilesUpdate)
		}
	}

	applyAction := &types.ApplyAction{
		Object:      obj,
		Description: a.Description,
		Progress:    0,
		Total:       total,
	}
	callback(applyAction)

	var err error

	// Process operations.
	for p, plan := range planMap {
		switch p.Operation {
		case types.PlanDelete:
			// Deletion.
			applyAction, err = b.applyDeletePlan(ctx, cli, plan, applyAction, callback)
			if err != nil {
				return err
			}

		case types.PlanUpdate:
			// Updates.
			applyAction, err = b.applyUpdatePlan(ctx, cli, plan, applyAction, callback)
			if err != nil {
				return err
			}

		case types.PlanAdd:
			// Creation.
			applyAction, err = b.applyCreatePlan(ctx, cli, plan, applyAction, callback)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
