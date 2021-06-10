package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/user"
	"strconv"

	"cloud.google.com/go/storage"
	plugin_util "github.com/outblocks/cli-plugin-gcp/internal/util"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/types"
	"github.com/outblocks/outblocks-plugin-go/validate"
	"google.golang.org/api/googleapi"
)

func (p *Plugin) defaultBucket(gcpProject string) string {
	return fmt.Sprintf("%s-%s", plugin_util.LimitString(plugin_util.SanitizeName(p.env.ProjectName()), 51), plugin_util.LimitString(plugin_util.SHAString(gcpProject), 8))
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

			return "", &plugin_go.LockErrorResponse{Owner: string(lockdata), LockInfo: strconv.FormatInt(attrs.Generation, 10)}
		}

		return "", fmt.Errorf("unable to acquire lock: %w", err)
	}

	return strconv.FormatInt(w.Attrs().Generation, 10), nil
}

func releaseLock(ctx context.Context, o *storage.ObjectHandle, lockID string) error {
	gen, err := strconv.ParseInt(lockID, 10, 64)
	if err != nil {
		return err
	}

	err = o.If(storage.Conditions{GenerationMatch: gen}).Delete(ctx)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			return fmt.Errorf("state lock already released")
		}

		if e, ok := err.(*googleapi.Error); ok && e.Code == http.StatusPreconditionFailed {
			return fmt.Errorf("state lock id doesn't match")
		}

		return err
	}

	return nil
}

func (p *Plugin) statefile(env string) string {
	return fmt.Sprintf("%s/%s/state", env, p.env.ProjectName())
}

func (p *Plugin) lockfile(env string) string {
	return fmt.Sprintf("%s/%s/lock", env, p.env.ProjectName())
}

func (p *Plugin) GetState(ctx context.Context, r *plugin_go.GetStateRequest) (plugin_go.Response, error) {
	res, project := validate.OptionalString(p.Settings.ProjectID, r.Properties, "project", "GCP project must be a string")
	if res != nil {
		return res, nil
	}

	res, bucket := validate.OptionalString(p.defaultBucket(project), r.Properties, "project", "bucket must be a string")
	if res != nil {
		return res, nil
	}

	pctx := p.PluginContext(ctx)

	cli, err := pctx.StorageClient()
	if err != nil {
		return nil, err
	}

	// Read state.
	b := cli.Bucket(bucket)
	created := false
	state, err := readBucketFile(ctx, b, p.statefile(r.Env))

	if err == storage.ErrObjectNotExist {
		created, err = ensureBucket(ctx, b, project, &storage.BucketAttrs{
			Location:          p.Settings.Region,
			VersioningEnabled: true,
		})

		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	// Lock if needed.
	var lockinfo string
	if r.Lock {
		lockinfo, err = acquireLock(ctx, b.Object(p.lockfile(r.Env)))
		if err != nil {
			return nil, err
		}
	}

	// Decode state.
	stateData := &types.StateData{}
	if len(state) > 0 {
		if err := json.Unmarshal(state, &stateData); err != nil {
			return nil, fmt.Errorf("cannot decode state file: %w", err)
		}
	}

	return &plugin_go.GetStateResponse{
		State:    stateData,
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

	res, bucket := validate.OptionalString(p.defaultBucket(project), r.Properties, "project", "bucket must be a string")
	if res != nil {
		return res, nil
	}

	pctx := p.PluginContext(ctx)

	cli, err := pctx.StorageClient()
	if err != nil {
		return nil, err
	}

	// Write state.
	b := cli.Bucket(bucket)
	w := b.Object(p.statefile(r.Env)).NewWriter(ctx)

	data, err := json.Marshal(r.State)
	if err != nil {
		return nil, err
	}

	_, err = w.Write(data)
	if err != nil {
		return nil, err
	}

	err = w.Close()
	if err != nil {
		return nil, err
	}

	return &plugin_go.SaveStateResponse{}, nil
}

func (p *Plugin) ReleaseLock(ctx context.Context, r *plugin_go.ReleaseLockRequest) (plugin_go.Response, error) {
	res, project := validate.OptionalString(p.Settings.ProjectID, r.Properties, "project", "GCP project must be a string")
	if res != nil {
		return res, nil
	}

	res, bucket := validate.OptionalString(p.defaultBucket(project), r.Properties, "project", "bucket must be a string")
	if res != nil {
		return res, nil
	}

	pctx := p.PluginContext(ctx)

	cli, err := pctx.StorageClient()
	if err != nil {
		return nil, err
	}

	b := cli.Bucket(bucket)
	err = releaseLock(ctx, b.Object(p.lockfile(r.Env)), r.LockID)

	return &plugin_go.EmptyResponse{}, err
}
