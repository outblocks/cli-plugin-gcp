package plugin

import (
	"context"
	"fmt"
	"io/ioutil"
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

func acquireLock(ctx context.Context, name string, o *storage.ObjectHandle) (string, error) {
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

			if name == "state" {
				return "", types.NewStateLockError(
					strconv.FormatInt(attrs.Generation, 10),
					string(lockdata),
					attrs.Created,
				)
			}

			return "", types.NewLockError(
				name,
				strconv.FormatInt(attrs.Generation, 10),
				string(lockdata),
				attrs.Created,
			)
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

	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return err
	}

	// Read state.
	b := cli.Bucket(bucket)

	created, err := ensureBucket(ctx, b, project, &storage.BucketAttrs{
		Location:          p.Settings.Region,
		VersioningEnabled: true,
	})
	if err != nil {
		return err
	}

	// Lock if needed.
	var lockinfo string

	if r.Lock {
		start := time.Now()
		t := time.NewTicker(time.Second)
		first := true
		lockWait := r.LockWait.AsDuration()

		defer t.Stop()

		for {
			lockinfo, err = acquireLock(ctx, "state", b.Object(p.lockfile("")))
			if err == nil {
				break
			}

			if err != nil && (lockWait == 0 || time.Since(start) > lockWait) {
				return err
			}

			if first {
				err = stream.Send(&apiv1.GetStateResponse{
					Response: &apiv1.GetStateResponse_Waiting{
						Waiting: true,
					},
				})
				if err != nil {
					return err
				}

				first = false
			}

			select {
			case <-ctx.Done():
				return err
			case <-t.C:
			}
		}
	}

	state, err := readBucketFile(ctx, b, p.statefile())
	if err != nil {
		if err != storage.ErrObjectNotExist {
			return err
		}

		created = true
	}

	return stream.Send(&apiv1.GetStateResponse{
		Response: &apiv1.GetStateResponse_State_{
			State: &apiv1.GetStateResponse_State{
				State:        state,
				LockInfo:     lockinfo,
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

	bucket, err := validate.OptionalString(p.defaultStateBucket(project), r.Properties.Fields, "bucket", "bucket must be a string")
	if err != nil {
		return nil, err
	}

	pctx := p.PluginContext()

	cli, err := pctx.StorageClient(ctx)
	if err != nil {
		return nil, err
	}

	b := cli.Bucket(bucket)
	err = releaseLock(ctx, b.Object(p.lockfile("")), r.LockInfo)

	return &apiv1.ReleaseStateLockResponse{}, err
}
