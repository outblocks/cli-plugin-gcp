package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/validate"
	"google.golang.org/api/googleapi"
)

func (p *Plugin) defaultBucket(gcpProject, suffix string) string {
	return fmt.Sprintf("%s%s-%s", plugin_util.LimitString(plugin_util.SanitizeName(p.env.ProjectName(), false, false), 51), suffix, plugin_util.LimitString(plugin_util.SHAString(gcpProject), 8))
}

func getBucket(ctx context.Context, b *storage.BucketHandle, project string, attrs *storage.BucketAttrs, create bool) (created, exists bool, err error) {
	_, err = b.Attrs(ctx)

	if err == storage.ErrBucketNotExist {
		if !create {
			return false, false, nil
		}

		if err := b.Create(ctx, project, attrs); err != nil {
			return false, false, fmt.Errorf("error creating GCS bucket: %w", err)
		}

		return true, true, nil
	}

	return false, true, err
}

func readBucketFile(ctx context.Context, b *storage.BucketHandle, file string) ([]byte, error) {
	r, err := b.Object(file).NewReader(ctx)
	if err != nil {
		return nil, err
	}

	return io.ReadAll(r)
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

var (
	errAcquireLockFailed   = errors.New("unable to acquire lock")
	errReleaseLockFailed   = errors.New("lock already released")
	errReleaseLockMismatch = errors.New("lock id doesn't match")
)

func acquireLock(ctx context.Context, o *storage.ObjectHandle) (lockinfo, owner string, createdAt time.Time, err error) {
	w := o.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	lockdata := lockdata()

	_, _ = w.Write([]byte(lockdata))

	err = w.Close()
	if err != nil { // nolint: nestif
		if e, ok := err.(*googleapi.Error); ok && e.Code == http.StatusPreconditionFailed {
			lockinfo, owner, createdAt, err := checkLock(ctx, o)
			if err != nil {
				return "", "", time.Time{}, fmt.Errorf("unable to acquire lock: %w", err)
			}

			return lockinfo, owner, createdAt, errAcquireLockFailed
		}

		return "", "", time.Time{}, fmt.Errorf("unable to acquire lock: %w", err)
	}

	return strconv.FormatInt(w.Attrs().Generation, 36), lockdata, w.Attrs().Created, nil
}

func checkLock(ctx context.Context, o *storage.ObjectHandle) (lockinfo, owner string, createdAt time.Time, err error) {
	r, err := o.NewReader(ctx)
	if err != nil {
		return "", "", time.Time{}, err
	}

	lockdata, err := io.ReadAll(r)
	if err != nil {
		return "", "", time.Time{}, err
	}

	attrs, err := o.Attrs(ctx)
	if err != nil {
		return "", "", time.Time{}, err
	}

	lockinfo = strconv.FormatInt(attrs.Generation, 36)

	return lockinfo, string(lockdata), attrs.Created, nil
}

func releaseLock(ctx context.Context, o *storage.ObjectHandle, lockID string) error {
	gen, err := strconv.ParseInt(lockID, 36, 64)
	if err != nil {
		return fmt.Errorf("invalid lock id")
	}

	err = o.If(storage.Conditions{GenerationMatch: gen}).Delete(ctx)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			return errReleaseLockFailed
		}

		if e, ok := err.(*googleapi.Error); ok && e.Code == http.StatusPreconditionFailed {
			return errReleaseLockMismatch
		}

		return err
	}

	return nil
}

func (p *Plugin) statefile() string {
	return fmt.Sprintf("%s/%s/state", p.env.Env(), p.env.ProjectName())
}

func (p *Plugin) stateLockfile() string {
	return fmt.Sprintf("%s/%s/lock", p.env.Env(), p.env.ProjectName())
}

func (p *Plugin) defaultStateBucket(project string) string {
	return p.defaultBucket(project, "")
}

func (p *Plugin) lockState(ctx context.Context, cli *storage.Client, project, lockingBucket string, lockWait time.Duration, stream apiv1.StatePluginService_GetStateServer) (string, error) {
	var lockInfo string

	lockingB := cli.Bucket(lockingBucket)

	_, _, err := getBucket(ctx, lockingB, project, &storage.BucketAttrs{
		Location:          p.Settings.Region,
		VersioningEnabled: false,
	}, true)
	if err != nil {
		return "", err
	}

	start := time.Now()
	t := time.NewTicker(time.Second)
	first := true

	var (
		owner     string
		createdAt time.Time
	)

	defer t.Stop()

	for {
		lockInfo, owner, createdAt, err = acquireLock(ctx, lockingB.Object(p.stateLockfile()))
		if err == nil {
			break
		}

		if err != nil && (lockWait == 0 || time.Since(start) > lockWait) {
			if err == errAcquireLockFailed {
				return "", types.NewStatusStateLockError(lockInfo, owner, createdAt)
			}

			return "", err
		}

		if first {
			err = stream.Send(&apiv1.GetStateResponse{
				Response: &apiv1.GetStateResponse_Waiting{
					Waiting: true,
				},
			})
			if err != nil {
				return "", err
			}

			first = false
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-t.C:
		}
	}

	return lockInfo, nil
}

func (p *Plugin) GetState(r *apiv1.GetStateRequest, stream apiv1.StatePluginService_GetStateServer) error {
	ctx := stream.Context()

	project, err := validate.OptionalString(p.Settings.ProjectID, r.Properties.Fields, "project", "GCP project must be a string")
	if err != nil {
		return err
	}

	bucket, err := validate.OptionalString(p.defaultStateBucket(project), r.Properties.Fields, "bucket", "bucket must be a string")
	if err != nil {
		return err
	}

	lockingBucket, err := validate.OptionalString(p.defaultLocksBucket(project), r.Properties.Fields, "locks_bucket", "locks bucket must be a string")
	if err != nil {
		return err
	}

	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	// Read state.
	b := cli.Bucket(bucket)

	created, exists, err := getBucket(ctx, b, project, &storage.BucketAttrs{
		Location:          p.Settings.Region,
		VersioningEnabled: true,
		Lifecycle: storage.Lifecycle{
			Rules: []storage.LifecycleRule{
				{
					Condition: storage.LifecycleCondition{
						DaysSinceNoncurrentTime: 14,
					},
					Action: storage.LifecycleAction{
						Type: "Delete",
					},
				},
				{
					Condition: storage.LifecycleCondition{
						NumNewerVersions: 100,
						Liveness:         storage.Archived,
					},
					Action: storage.LifecycleAction{
						Type: "Delete",
					},
				},
			},
		},
	}, !r.SkipCreate)
	if err != nil {
		return err
	}

	if !exists && r.SkipCreate {
		return stream.Send(&apiv1.GetStateResponse{
			Response: &apiv1.GetStateResponse_State_{
				State: &apiv1.GetStateResponse_State{
					StateCreated: false,
					StateName:    bucket,
				},
			},
		})
	}

	// Lock if needed.
	var lockInfo string

	if r.Lock {
		lockInfo, err = p.lockState(ctx, cli, project, lockingBucket, r.LockWait.AsDuration(), stream)
		if err != nil {
			return err
		}
	}

	state, err := readBucketFile(ctx, b, p.statefile())
	if err != nil {
		if err != storage.ErrObjectNotExist {
			return err
		}

		if r.SkipCreate {
			return stream.Send(&apiv1.GetStateResponse{
				Response: &apiv1.GetStateResponse_State_{
					State: &apiv1.GetStateResponse_State{
						StateCreated: false,
						StateName:    bucket,
					},
				},
			})
		}

		created = true
	}

	return stream.Send(&apiv1.GetStateResponse{
		Response: &apiv1.GetStateResponse_State_{
			State: &apiv1.GetStateResponse_State{
				State:        state,
				LockInfo:     lockInfo,
				StateCreated: created,
				StateName:    bucket,
			},
		},
	})
}

func (p *Plugin) SaveState(ctx context.Context, r *apiv1.SaveStateRequest) (*apiv1.SaveStateResponse, error) {
	project, err := validate.OptionalString(p.Settings.ProjectID, r.Properties.Fields, "project", "GCP project must be a string")
	if err != nil {
		return nil, err
	}

	bucket, err := validate.OptionalString(p.defaultStateBucket(project), r.Properties.Fields, "bucket", "bucket must be a string")
	if err != nil {
		return nil, err
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

	return &apiv1.SaveStateResponse{}, nil
}

func (p *Plugin) ReleaseStateLock(ctx context.Context, r *apiv1.ReleaseStateLockRequest) (*apiv1.ReleaseStateLockResponse, error) {
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
	err = releaseLock(ctx, b.Object(p.stateLockfile()), r.LockInfo)

	return &apiv1.ReleaseStateLockResponse{}, err
}
