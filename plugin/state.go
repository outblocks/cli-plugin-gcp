package plugin

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/user"
	"sort"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/validate"
	"google.golang.org/api/googleapi"
)

func (p *Plugin) defaultBucket(gcpProject, suffix string) string {
	return fmt.Sprintf("%s%s-%s", plugin_util.LimitString(plugin_util.SanitizeName(p.env.ProjectName()), 51), suffix, plugin_util.LimitString(plugin_util.SHAString(gcpProject), 8))
}

func ensureBucket(ctx context.Context, b *storage.BucketHandle, project string, attrs *storage.BucketAttrs) (bool, error) {
	_, err := b.Attrs(ctx)

	if err == storage.ErrBucketNotExist {
		if err := b.Create(ctx, project, attrs); err != nil {
			return false, fmt.Errorf("error creating GCS bucket: %w", err)
		}

		return true, nil
	}

	return false, err
}

func readBucketFile(ctx context.Context, b *storage.BucketHandle, file string) ([]byte, error) {
	r, err := b.Object(file).NewReader(ctx)
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

func lockdata() string {
	username := ""

	u, _ := user.Current()
	if u != nil {
		username = u.Username
	}

	host, _ := os.Hostname()

	return fmt.Sprintf("%s@%s", username, host)
}

func acquireLock(ctx context.Context, o *storage.ObjectHandle) (string, error) {
	w := o.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	lockdata := lockdata()

	_, _ = w.Write([]byte(lockdata))

	err := w.Close()
	if err != nil { // nolint: nestif
		if e, ok := err.(*googleapi.Error); ok && e.Code == http.StatusPreconditionFailed {
			r, err := o.NewReader(ctx)
			if err != nil {
				return "", err
			}

			lockdata, err := ioutil.ReadAll(r)
			if err != nil {
				return "", err
			}

			attrs, err := o.Attrs(ctx)
			if err != nil {
				return "", err
			}

			return "", &plugin_go.LockErrorResponse{
				Owner:     string(lockdata),
				CreatedAt: attrs.Created,
				LockInfo:  strconv.FormatInt(attrs.Generation, 10),
			}
		}

		return "", fmt.Errorf("unable to acquire lock: %w", err)
	}

	return strconv.FormatInt(w.Attrs().Generation, 10), nil
}

func releaseLock(ctx context.Context, o *storage.ObjectHandle, lockID string) error {
	gen, err := strconv.ParseInt(lockID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid lock id")
	}

	err = o.If(storage.Conditions{GenerationMatch: gen}).Delete(ctx)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			return fmt.Errorf("lock already released")
		}

		if e, ok := err.(*googleapi.Error); ok && e.Code == http.StatusPreconditionFailed {
			return fmt.Errorf("lock id doesn't match")
		}

		return err
	}

	return nil
}

func (p *Plugin) statefile() string {
	return fmt.Sprintf("%s/%s/state", p.env.Env(), p.env.ProjectName())
}

func (p *Plugin) lockfile(name string) string {
	if name != "" {
		sanitizedName := fmt.Sprintf("%s-%s", plugin_util.SanitizeName(name), plugin_util.LimitString(plugin_util.SHAString(name), 4))
		return fmt.Sprintf("%s/%s/locks/%s", p.env.Env(), p.env.ProjectName(), sanitizedName)
	}

	return fmt.Sprintf("%s/%s/lock", p.env.Env(), p.env.ProjectName())
}

func (p *Plugin) defaultStateBucket(project string) string {
	return p.defaultBucket(project, "")
}

func (p *Plugin) GetState(ctx context.Context, r *plugin_go.GetStateRequest) (plugin_go.Response, error) {
	res, project := validate.OptionalString(p.Settings.ProjectID, r.Properties, "project", "GCP project must be a string")
	if res != nil {
		return res, nil
	}

	res, bucket := validate.OptionalString(p.defaultStateBucket(project), r.Properties, "bucket", "bucket must be a string")
	if res != nil {
		return res, nil
	}

	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return nil, err
	}

	// Read state.
	b := cli.Bucket(bucket)

	created, err := ensureBucket(ctx, b, project, &storage.BucketAttrs{
		Location:          p.Settings.Region,
		VersioningEnabled: true,
	})
	if err != nil {
		return nil, err
	}

	// Lock if needed.
	var lockinfo string

	if r.Lock {
		start := time.Now()
		t := time.NewTicker(time.Second)
		first := true

		defer t.Stop()

		for {
			lockinfo, err = acquireLock(ctx, b.Object(p.lockfile("")))
			if err == nil {
				break
			}

			if err != nil && (r.LockWait == 0 || time.Since(start) > r.LockWait) {
				return nil, err
			}

			if first {
				p.log.Infoln("Lock is acquired. Waiting for it to be free...")

				first = false
			}

			select {
			case <-ctx.Done():
				return nil, err
			case <-t.C:
			}
		}
	}

	state, err := readBucketFile(ctx, b, p.statefile())
	if err != nil {
		return nil, err
	}

	return &plugin_go.GetStateResponse{
		State:    state,
		LockInfo: lockinfo,
		Source: &types.StateSource{
			Name:    bucket,
			Created: created,
		},
	}, nil
}

func (p *Plugin) SaveState(ctx context.Context, r *plugin_go.SaveStateRequest) (plugin_go.Response, error) {
	res, project := validate.OptionalString(p.Settings.ProjectID, r.Properties, "project", "GCP project must be a string")
	if res != nil {
		return res, nil
	}

	res, bucket := validate.OptionalString(p.defaultStateBucket(project), r.Properties, "project", "bucket must be a string")
	if res != nil {
		return res, nil
	}

	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return nil, err
	}

	// Write state.
	b := cli.Bucket(bucket)
	w := b.Object(p.statefile()).NewWriter(ctx)

	_, err = w.Write(r.State)
	if err != nil {
		return nil, err
	}

	err = w.Close()
	if err != nil {
		return nil, err
	}

	return &plugin_go.SaveStateResponse{}, nil
}

func (p *Plugin) ReleaseStateLock(ctx context.Context, r *plugin_go.ReleaseStateLockRequest) (plugin_go.Response, error) {
	res, project := validate.OptionalString(p.Settings.ProjectID, r.Properties, "project", "GCP project must be a string")
	if res != nil {
		return res, nil
	}

	res, bucket := validate.OptionalString(p.defaultStateBucket(project), r.Properties, "project", "bucket must be a string")
	if res != nil {
		return res, nil
	}

	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return nil, err
	}

	b := cli.Bucket(bucket)
	err = releaseLock(ctx, b.Object(p.lockfile("")), r.LockInfo)

	return &plugin_go.EmptyResponse{}, err
}

// Locking.

func (p *Plugin) defaultLocksBucket(project string) string {
	return p.defaultBucket(project, "-locks")
}

func (p *Plugin) AcquireLocks(ctx context.Context, r *plugin_go.AcquireLocksRequest) (plugin_go.Response, error) {
	res, project := validate.OptionalString(p.Settings.ProjectID, r.Properties, "project", "GCP project must be a string")
	if res != nil {
		return res, nil
	}

	bucket := p.defaultLocksBucket(project)
	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return nil, err
	}

	b := cli.Bucket(bucket)

	_, err = ensureBucket(ctx, b, project, &storage.BucketAttrs{
		Location: p.Settings.Region,
	})
	if err != nil {
		return nil, err
	}

	start := time.Now()
	t := time.NewTicker(time.Second)

	defer t.Stop()

	lockfiles := make([]string, len(r.LockNames))

	for i, n := range r.LockNames {
		lockfiles[i] = p.lockfile(n)
	}

	sort.Strings(lockfiles)

	lockInfos := make([]string, 0, len(lockfiles))

	for _, lockfile := range lockfiles {
		lockObject := b.Object(lockfile)

		for {
			lockInfo, err := acquireLock(ctx, lockObject)
			if err == nil {
				lockInfos = append(lockInfos, lockInfo)
			}

			if err != nil && (r.LockWait == 0 || time.Since(start) > r.LockWait) {
				return nil, err
			}

			select {
			case <-ctx.Done():
				return nil, err
			case <-t.C:
			}
		}
	}

	return &plugin_go.LocksAcquiredResponse{LockInfo: lockInfos}, nil
}

func (p *Plugin) ReleaseLocks(ctx context.Context, r *plugin_go.ReleaseLocksRequest) (plugin_go.Response, error) {
	res, project := validate.OptionalString(p.Settings.ProjectID, r.Properties, "project", "GCP project must be a string")
	if res != nil {
		return res, nil
	}

	bucket := p.defaultLocksBucket(project)
	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return nil, err
	}

	b := cli.Bucket(bucket)

	lockMap := make(map[string]string, len(r.Locks))

	for name, info := range r.Locks {
		lockMap[p.lockfile(name)] = info
	}

	var firstErr error

	for name, info := range lockMap {
		err = releaseLock(ctx, b.Object(name), info)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return &plugin_go.EmptyResponse{}, err
}
