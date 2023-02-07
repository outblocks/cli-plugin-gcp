package plugin

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/env"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
	"google.golang.org/api/secretmanager/v1"
)

func (p *Plugin) initSecrets(ctx context.Context) (*secretmanager.Service, error) {
	cli, err := config.NewGCPSecretManagerClient(ctx, p.gcred)
	if err != nil {
		return nil, fmt.Errorf("error creating gcp secret manager client: %w", err)
	}

	return cli, nil
}

func getSecret(cli *secretmanager.Service, project, name string) (ok bool, err error) {
	_, err = cli.Projects.Secrets.Get(fmt.Sprintf("projects/%s/secrets/%s", project, name)).Do()
	if gcp.ErrIs404(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}

func createSecret(cli *secretmanager.Service, e env.Enver, project, key string) error {
	_, err := cli.Projects.Secrets.Create(fmt.Sprintf("projects/%s", project), &secretmanager.Secret{
		Labels: map[string]string{
			"creator": "outblocks",
			"env":     e.Env(),
			"project": e.ProjectName(),
		},
		Replication: &secretmanager.Replication{
			Automatic: &secretmanager.Automatic{},
		},
	}).SecretId(key).Do()
	if err != nil {
		return fmt.Errorf("error creating secret '%s': %w", key, err)
	}

	return nil
}

func accessSecretValue(cli *secretmanager.Service, project, name string) (val string, ok bool, err error) {
	ret, err := cli.Projects.Secrets.Versions.Access(fmt.Sprintf("projects/%s/secrets/%s/versions/latest", project, name)).Do()
	if gcp.ErrIs404(err) {
		return "", false, nil
	}

	if gcp.ErrIs400(err) {
		return "", true, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("error accessing secret '%s' value: %w", name, err)
	}

	data, _ := base64.StdEncoding.DecodeString(ret.Payload.Data)

	return string(data), true, nil
}

func listSecrets(cli *secretmanager.Service, project, prefix string) ([]string, error) {
	ret, err := cli.Projects.Secrets.List(fmt.Sprintf("projects/%s", project)).Do()
	if err != nil {
		return nil, fmt.Errorf("error listing secrets: %w", err)
	}

	var secrets []string

	for _, s := range ret.Secrets {
		parts := strings.SplitN(s.Name, "/", 4)
		name := parts[3]

		if !strings.HasPrefix(name, prefix) {
			continue
		}

		secrets = append(secrets, name)
	}

	return secrets, nil
}

func setSecretValue(cli *secretmanager.Service, project, name, value string, cleanup bool) error {
	secretPath := fmt.Sprintf("projects/%s/secrets/%s", project, name)

	now := time.Now()

	if value != "" {
		newVer, err := cli.Projects.Secrets.AddVersion(secretPath, &secretmanager.AddSecretVersionRequest{
			Payload: &secretmanager.SecretPayload{
				Data: base64.StdEncoding.EncodeToString([]byte(value)),
			},
		}).Do()

		if err != nil {
			return fmt.Errorf("error setting secret '%s' value: %w", name, err)
		}

		now, _ = time.Parse(time.RFC3339, newVer.CreateTime)
	}

	if !cleanup {
		return nil
	}

	versions, err := cli.Projects.Secrets.Versions.List(secretPath).Do()
	if err != nil {
		return fmt.Errorf("error listing secret '%s' versions: %w", name, err)
	}

	for _, v := range versions.Versions {
		verTime, _ := time.Parse(time.RFC3339, v.CreateTime)

		if !verTime.Before(now) || v.State != "ENABLED" { //nolint: gocritic
			continue
		}

		_, err = cli.Projects.Secrets.Versions.Destroy(v.Name, &secretmanager.DestroySecretVersionRequest{}).Do()
		if err != nil {
			return fmt.Errorf("error deleting old secret '%s' version: %w", name, err)
		}
	}

	return nil
}

func deleteSecret(cli *secretmanager.Service, project, name string) (ok bool, err error) {
	secretPath := fmt.Sprintf("projects/%s/secrets/%s", project, name)

	_, err = cli.Projects.Secrets.Delete(secretPath).Do()

	if gcp.ErrIs404(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("error deleting secret '%s': %w", name, err)
	}

	return true, nil
}

func (p *Plugin) secretKeyPrefix() string {
	return fmt.Sprintf("OUTBLOCKS_%s_", util.SanitizeName(p.env.ProjectID(), false, false))
}

func (p *Plugin) secretKey(key string) (string, error) {
	sanitized := util.SanitizeName(key, true, false)
	if sanitized != key {
		return "", fmt.Errorf("secret names can only contain English letters (A-Z), numbers (0-9), dashes (-), and underscores (_)")
	}

	return fmt.Sprintf("%s%s", p.secretKeyPrefix(), sanitized), nil
}

func (p *Plugin) GetSecret(ctx context.Context, req *apiv1.GetSecretRequest) (*apiv1.GetSecretResponse, error) {
	cli, err := p.initSecrets(ctx)
	if err != nil {
		return nil, err
	}

	key, err := p.secretKey(req.Key)
	if err != nil {
		return nil, err
	}

	var (
		val string
		ok  bool
	)

	err = p.runAndEnsureAPI(ctx, func() error {
		val, ok, err = accessSecretValue(cli, p.settings.ProjectID, key)
		return err
	})
	if err != nil {
		return nil, err
	}

	return &apiv1.GetSecretResponse{
		Value:     val,
		Specified: ok,
	}, nil
}

func (p *Plugin) SetSecret(ctx context.Context, req *apiv1.SetSecretRequest) (*apiv1.SetSecretResponse, error) {
	cli, err := p.initSecrets(ctx)
	if err != nil {
		return nil, err
	}

	key, err := p.secretKey(req.Key)
	if err != nil {
		return nil, err
	}

	var ok bool

	err = p.runAndEnsureAPI(ctx, func() error {
		ok, err = getSecret(cli, p.settings.ProjectID, key)
		return err
	})
	if err != nil {
		return nil, err
	}

	cleanup := true

	if !ok {
		err = createSecret(cli, p.env, p.settings.ProjectID, key)
		if err != nil {
			return nil, err
		}

		cleanup = false
	} else {
		existingVal, _, err := accessSecretValue(cli, p.settings.ProjectID, key)
		if err != nil {
			return nil, err
		}

		if existingVal == req.Value {
			return &apiv1.SetSecretResponse{
				Changed: false,
			}, nil
		}
	}

	err = setSecretValue(cli, p.settings.ProjectID, key, req.Value, cleanup)
	if err != nil {
		return nil, err
	}

	return &apiv1.SetSecretResponse{
		Changed: true,
	}, nil
}

func (p *Plugin) DeleteSecret(ctx context.Context, req *apiv1.DeleteSecretRequest) (*apiv1.DeleteSecretResponse, error) {
	cli, err := p.initSecrets(ctx)
	if err != nil {
		return nil, err
	}

	key, err := p.secretKey(req.Key)
	if err != nil {
		return nil, err
	}

	var ok bool

	err = p.runAndEnsureAPI(ctx, func() error {
		ok, err = deleteSecret(cli, p.settings.ProjectID, key)
		return err
	})
	if err != nil {
		return nil, err
	}

	return &apiv1.DeleteSecretResponse{
		Deleted: ok,
	}, nil
}

func (p *Plugin) GetSecrets(ctx context.Context, req *apiv1.GetSecretsRequest) (*apiv1.GetSecretsResponse, error) {
	cli, err := p.initSecrets(ctx)
	if err != nil {
		return nil, err
	}

	prefix := p.secretKeyPrefix()

	var secrets []string

	err = p.runAndEnsureAPI(ctx, func() error {
		secrets, err = listSecrets(cli, p.settings.ProjectID, prefix)
		return err
	})
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex

	values := make(map[string]string)
	g, _ := errgroup.WithConcurrency(ctx, gcp.DefaultConcurrency)

	for _, v := range secrets {
		v := v

		g.Go(func() error {
			val, _, err := accessSecretValue(cli, p.settings.ProjectID, v)
			if err != nil {
				return err
			}

			mu.Lock()
			values[v[len(prefix):]] = val
			mu.Unlock()

			return nil
		})
	}

	err = g.Wait()
	if err != nil {
		return nil, err
	}

	return &apiv1.GetSecretsResponse{
		Values: values,
	}, nil
}

func (p *Plugin) ReplaceSecrets(ctx context.Context, req *apiv1.ReplaceSecretsRequest) (*apiv1.ReplaceSecretsResponse, error) {
	cli, err := p.initSecrets(ctx)
	if err != nil {
		return nil, err
	}

	prefix := p.secretKeyPrefix()
	values := make(map[string]string, len(req.Values))

	for k, v := range req.Values {
		key, err := p.secretKey(k)
		if err != nil {
			return nil, err
		}

		values[key] = v
	}

	var secrets []string

	err = p.runAndEnsureAPI(ctx, func() error {
		secrets, err = listSecrets(cli, p.settings.ProjectID, prefix)
		return err
	})
	if err != nil {
		return nil, err
	}

	g, _ := errgroup.WithConcurrency(ctx, gcp.DefaultConcurrency)

	for _, cur := range secrets {
		cur := cur

		val, ok := values[cur]
		if !ok {
			g.Go(func() error {
				_, err = deleteSecret(cli, p.settings.ProjectID, cur)

				return err
			})

			continue
		}

		g.Go(func() error {
			existingVal, existingSet, err := accessSecretValue(cli, p.settings.ProjectID, cur)
			if err != nil {
				return err
			}

			if !existingSet || existingVal != val {
				err = setSecretValue(cli, p.settings.ProjectID, cur, val, existingSet)
				if err != nil {
					return err
				}
			}

			return nil
		})

		delete(values, cur)
	}

	for k, v := range values {
		k := k
		v := v

		g.Go(func() error {
			err := createSecret(cli, p.env, p.settings.ProjectID, k)
			if err != nil {
				return err
			}

			err = setSecretValue(cli, p.settings.ProjectID, k, v, false)

			return err
		})
	}

	err = g.Wait()
	if err != nil {
		return nil, err
	}

	return &apiv1.ReplaceSecretsResponse{}, nil
}

func (p *Plugin) DeleteSecrets(ctx context.Context, req *apiv1.DeleteSecretsRequest) (*apiv1.DeleteSecretsResponse, error) {
	cli, err := p.initSecrets(ctx)
	if err != nil {
		return nil, err
	}

	prefix := p.secretKeyPrefix()

	var secrets []string

	err = p.runAndEnsureAPI(ctx, func() error {
		secrets, err = listSecrets(cli, p.settings.ProjectID, prefix)
		return err
	})
	if err != nil {
		return nil, err
	}

	g, _ := errgroup.WithConcurrency(ctx, gcp.DefaultConcurrency)

	for _, cur := range secrets {
		cur := cur

		g.Go(func() error {
			_, err = deleteSecret(cli, p.settings.ProjectID, cur)

			return err
		})
	}

	err = g.Wait()
	if err != nil {
		return nil, err
	}

	return &apiv1.DeleteSecretsResponse{}, nil
}
