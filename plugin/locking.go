package plugin

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
	"github.com/outblocks/outblocks-plugin-go/validate"
)

func (p *Plugin) defaultLocksBucket(project string) string {
	return p.defaultBucket(project, "-locks")
}

func (p *Plugin) lockfile(name string) string {
	name = plugin_util.SanitizeName(name, true, true)
	if len(name) > 44 {
		name = fmt.Sprintf("%s-%s", plugin_util.LimitString(name, 40), plugin_util.LimitString(plugin_util.SHAString(name), 4))
	}

	return fmt.Sprintf("%s/%s/%s", p.env.Env(), p.env.ProjectName(), name)
}

func (p *Plugin) acquireLocks(ctx context.Context, lockfiles []string, lockNamesMap map[string]string, lockWait time.Duration, b *storage.BucketHandle, waitCb func() error) (locksAcquired map[string]string, lockInfoFailed []*apiv1.LockError, err error) {
	var mu sync.Mutex

	t := time.NewTicker(time.Second)
	start := time.Now()
	locksAcquired = make(map[string]string)

	for i, lockfile := range lockfiles {
		lockObject := b.Object(lockfile)
		name := lockNamesMap[lockfile]

		for {
			lockInfo, owner, createdAt, err := acquireLock(ctx, lockObject)
			if err == nil {
				locksAcquired[name] = lockInfo

				break
			}

			if err != nil && (lockWait == 0 || time.Since(start) > lockWait) {
				if err != errAcquireLockFailed {
					return nil, nil, err
				}

				lockInfoFailed = append(lockInfoFailed, types.NewLockError(name, lockInfo, owner, createdAt))

				g, _ := errgroup.WithConcurrency(ctx, gcp.DefaultConcurrency)

				if len(lockfiles) > i {
					for _, lockfile := range lockfiles[i+1:] {
						lockObject := b.Object(lockfile)
						name := lockNamesMap[lockfile]

						g.Go(func() error {
							lockInfo, owner, createdAt, err := checkLock(ctx, lockObject)
							if err != nil && err != storage.ErrObjectNotExist {
								return err
							}

							mu.Lock()
							lockInfoFailed = append(lockInfoFailed, types.NewLockError(name, lockInfo, owner, createdAt))
							mu.Unlock()

							return nil
						})
					}
				}

				return locksAcquired, lockInfoFailed, g.Wait()
			}

			if waitCb != nil {
				err = waitCb()
				if err != nil {
					return nil, nil, err
				}
			}

			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-t.C:
			}
		}
	}

	return locksAcquired, lockInfoFailed, nil
}

func (p *Plugin) AcquireLocks(r *apiv1.AcquireLocksRequest, stream apiv1.LockingPluginService_AcquireLocksServer) error {
	ctx := stream.Context()

	project, err := validate.OptionalString(p.Settings.ProjectID, r.Properties.Fields, "project", "GCP project must be a string")
	if err != nil {
		return err
	}

	bucket, err := validate.OptionalString(p.defaultLocksBucket(project), r.Properties.Fields, "locks_bucket", "locks bucket must be a string")
	if err != nil {
		return err
	}

	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	b := cli.Bucket(bucket)

	_, _, err = getBucket(ctx, b, project, &storage.BucketAttrs{
		Location: p.Settings.Region,
	}, true)
	if err != nil {
		return err
	}

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

	var first bool

	locksAcquired, lockInfoFailed, err := p.acquireLocks(ctx, lockfiles, lockNamesMap, lockWait, b, func() error {
		if first {
			err = stream.Send(&apiv1.AcquireLocksResponse{
				Waiting: true,
			})
			if err != nil {
				return err
			}

			first = false
		}

		return nil
	})

	if len(lockInfoFailed) > 0 {
		if len(locksAcquired) > 0 {
			_, _ = p.ReleaseLocks(context.Background(), &apiv1.ReleaseLocksRequest{
				Locks:      locksAcquired,
				Properties: r.Properties,
			})
		}

		err = types.NewStatusLockError(lockInfoFailed...)
	}

	if err != nil {
		return err
	}

	return stream.Send(&apiv1.AcquireLocksResponse{
		Locks: locksAcquired,
	})
}

func (p *Plugin) ReleaseLocks(ctx context.Context, r *apiv1.ReleaseLocksRequest) (*apiv1.ReleaseLocksResponse, error) {
	project, err := validate.OptionalString(p.Settings.ProjectID, r.Properties.Fields, "project", "GCP project must be a string")
	if err != nil {
		return nil, err
	}

	bucket, err := validate.OptionalString(p.defaultLocksBucket(project), r.Properties.Fields, "locks_bucket", "locks bucket must be a string")
	if err != nil {
		return nil, err
	}

	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return nil, err
	}

	b := cli.Bucket(bucket)
	g, _ := errgroup.WithConcurrency(ctx, gcp.DefaultConcurrency)

	for name, info := range r.Locks {
		name := name
		info := info

		g.Go(func() error {
			return releaseLock(ctx, b.Object(p.lockfile(name)), info)
		})
	}

	return &apiv1.ReleaseLocksResponse{}, g.Wait()
}
