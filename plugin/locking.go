package plugin

import (
	"context"
	"sort"
	"time"

	"cloud.google.com/go/storage"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/validate"
)

func (p *Plugin) defaultLocksBucket(project string) string {
	return p.defaultBucket(project, "-locks")
}

func (p *Plugin) AcquireLocks(ctx context.Context, r *apiv1.AcquireLocksRequest) (*apiv1.AcquireLocksResponse, error) {
	project, err := validate.OptionalString(p.Settings.ProjectID, r.Properties.Fields, "project", "GCP project must be a string")
	if err != nil {
		return nil, err
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
	lockWait := r.LockWait.AsDuration()

	defer t.Stop()

	lockNamesMap := make(map[string]string, len(r.LockNames))
	lockfiles := make([]string, len(r.LockNames))

	for i, n := range r.LockNames {
		val := p.lockfile(n)
		lockNamesMap[val] = n
		lockfiles[i] = val
	}

	sort.Strings(lockfiles)

	lockInfos := make([]string, 0, len(lockfiles))

	for _, lockfile := range lockfiles {
		lockObject := b.Object(lockfile)

		for {
			lockInfo, err := acquireLock(ctx, lockNamesMap[lockfile], lockObject)
			if err == nil {
				lockInfos = append(lockInfos, lockInfo)
			}

			if err != nil && (lockWait == 0 || time.Since(start) > lockWait) {
				return nil, err
			}

			select {
			case <-ctx.Done():
				return nil, err
			case <-t.C:
			}
		}
	}

	return &apiv1.AcquireLocksResponse{LockInfo: lockInfos}, nil
}

func (p *Plugin) ReleaseLocks(ctx context.Context, r *apiv1.ReleaseLocksRequest) (*apiv1.ReleaseLocksResponse, error) {
	project, err := validate.OptionalString(p.Settings.ProjectID, r.Properties.Fields, "project", "GCP project must be a string")
	if err != nil {
		return nil, err
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

	return &apiv1.ReleaseLocksResponse{}, err
}
