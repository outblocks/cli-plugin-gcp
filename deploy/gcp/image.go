package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	dockertypes "github.com/docker/docker/api/types"
	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	gcrname "github.com/google/go-containerregistry/pkg/name"
	gcrremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
)

type Image struct {
	registry.ResourceBase

	Name      fields.StringInputField `state:"force_new"`
	ProjectID fields.StringInputField `state:"force_new"`
	GCR       fields.StringInputField `state:"force_new"`
	Source    fields.StringInputField `state:"force_new"`
}

func (o *Image) GetName() string {
	return o.Name.Any()
}

func (o *Image) imageName(gcr, projectID, name string) string {
	return fmt.Sprintf("%s/%s/%s", gcr, projectID, name)
}

func (o *Image) ImageName() fields.StringInputField {
	return fields.Sprintf("%s/%s/%s", o.GCR, o.ProjectID, o.Name)
}

func (o *Image) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return fmt.Errorf("error getting google credentials token: %w", err)
	}

	gcr := o.GCR.Any()
	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	imagenameparts := strings.SplitN(o.imageName(gcr, projectID, name), ":", 2)
	if len(imagenameparts) < 2 {
		return nil
	}

	imagename, tag := imagenameparts[0], imagenameparts[1]

	gcrrepo, err := gcrname.NewRepository(imagename)
	if err != nil {
		return err
	}

	ref := gcrrepo.Tag(tag)
	auth := &gcrauthn.Bearer{
		Token: token.AccessToken,
	}

	_, err = gcrremote.Head(ref, gcrremote.WithAuth(auth), gcrremote.WithContext(ctx))
	if ErrIs404(err) {
		o.SetNew(true)

		return nil
	} else if err != nil {
		return fmt.Errorf("error fetching image status: %w", err)
	}

	o.SetNew(false)
	o.Name.SetCurrent(name)
	o.GCR.SetCurrent(gcr)
	o.ProjectID.SetCurrent(projectID)
	o.Source.SetCurrent(o.Source.Any())

	return nil
}

func (o *Image) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return fmt.Errorf("error getting google credentials token: %w", err)
	}

	cli, err := pctx.DockerClient()
	if err != nil {
		return err
	}

	reader, err := cli.ImagePull(ctx, GCSProxyDockerImage, dockertypes.ImagePullOptions{})
	if err != nil {
		return err
	}

	_, err = io.Copy(io.Discard, reader)
	if err != nil {
		return err
	}

	err = reader.Close()
	if err != nil {
		return err
	}

	err = cli.ImageTag(ctx, GCSProxyDockerImage, o.ImageName().Wanted())
	if err != nil {
		return err
	}

	authConfig := dockertypes.AuthConfig{
		Username: "oauth2accesstoken",
		Password: token.AccessToken,
	}

	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return err
	}

	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	reader, err = cli.ImagePush(ctx, o.ImageName().Wanted(), dockertypes.ImagePushOptions{
		RegistryAuth: authStr,
	})
	if err != nil {
		return err
	}

	_, err = io.Copy(io.Discard, reader)
	if err != nil {
		return err
	}

	err = reader.Close()
	if err != nil {
		return err
	}

	return reader.Close()
}

func (o *Image) Update(ctx context.Context, meta interface{}) error {
	return fmt.Errorf("unimplemented")
}

func (o *Image) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return fmt.Errorf("error getting google credentials token: %w", err)
	}

	names := strings.SplitN(o.ImageName().Current(), ":", 2)
	if len(names) < 2 {
		return nil
	}

	name, tag := names[0], names[1]

	gcrrepo, err := gcrname.NewRepository(name)
	if err != nil {
		return err
	}

	ref := gcrrepo.Tag(tag)
	auth := &gcrauthn.Bearer{
		Token: token.AccessToken,
	}

	return gcrremote.Delete(ref, gcrremote.WithAuth(auth), gcrremote.WithContext(ctx))
}
