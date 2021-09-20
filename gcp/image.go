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
	v1 "github.com/google/go-containerregistry/pkg/v1"
	gcrremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
)

type Image struct {
	registry.ResourceBase

	Name       fields.StringInputField
	Tag        fields.StringInputField
	ProjectID  fields.StringInputField `state:"force_new"`
	GCR        fields.StringInputField `state:"force_new"`
	Digest     fields.StringOutputField
	Source     fields.StringInputField
	SourceHash fields.StringInputField

	Pull bool `state:"-"`
}

func (o *Image) GetName() string {
	tag := o.Tag.Any()
	name := o.Name.Any()

	if tag != "" {
		name = fmt.Sprintf("%s:%s", name, tag)
	}

	return name
}

func (o *Image) imageName(gcr, projectID, name string) string {
	return fmt.Sprintf("%s/%s/%s", gcr, projectID, name)
}

func (o *Image) ImageName() fields.StringInputField {
	return fields.Sprintf("%s/%s/%s@%s", o.GCR, o.ProjectID, o.Name, o.Digest)
}

func (o *Image) readImage(ctx context.Context, meta interface{}) (*v1.Descriptor, error) {
	pctx := meta.(*config.PluginContext)

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("error getting google credentials token: %w", err)
	}

	gcr := o.GCR.Any()
	projectID := o.ProjectID.Any()
	name := o.Name.Any()
	tag := o.Tag.Any()

	gcrrepo, err := gcrname.NewRepository(o.imageName(gcr, projectID, name))
	if err != nil {
		return nil, err
	}

	auth := &gcrauthn.Bearer{
		Token: token.AccessToken,
	}

	var ref gcrname.Reference

	if tag != "" {
		ref = gcrrepo.Tag(tag)
	} else {
		if digest, ok := o.Digest.LookupCurrent(); ok {
			ref = gcrrepo.Digest(digest)
		} else {
			ref = gcrrepo.Tag("latest")
		}
	}

	return gcrremote.Head(ref, gcrremote.WithAuth(auth), gcrremote.WithContext(ctx))
}

func (o *Image) Read(ctx context.Context, meta interface{}) error {
	desc, err := o.readImage(ctx, meta)
	if ErrIs404(err) {
		o.Digest.Invalidate()
		o.MarkAsNew()

		return nil
	} else if err != nil {
		return fmt.Errorf("error fetching image status: %w", err)
	}

	o.MarkAsExisting()
	o.Name.SetCurrent(o.Name.Any())
	o.Tag.SetCurrent(o.Tag.Any())
	o.Digest.SetCurrent(desc.Digest.String())
	o.GCR.SetCurrent(o.GCR.Any())
	o.ProjectID.SetCurrent(o.ProjectID.Any())
	o.Source.SetCurrent(o.Source.Any())

	return nil
}

func (o *Image) BeforeDiff() {
	if o.Source.IsChanged() || o.SourceHash.IsChanged() {
		o.Digest.Invalidate()
	}
}

func (o *Image) Create(ctx context.Context, meta interface{}) error {
	return o.push(ctx, meta)
}

func (o *Image) Update(ctx context.Context, meta interface{}) error {
	return o.push(ctx, meta)
}

func (o *Image) push(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return fmt.Errorf("error getting google credentials token: %w", err)
	}

	cli, err := pctx.DockerClient()
	if err != nil {
		return err
	}

	_, err = cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker is required for GCR image upload!\n%w", err)
	}

	if o.Pull {
		// Pull image from source.
		reader, err := cli.ImagePull(ctx, o.Source.Wanted(), dockertypes.ImagePullOptions{})
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
	}

	gcr := o.GCR.Wanted()
	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()
	tag := o.Tag.Wanted()
	imageName := o.imageName(gcr, projectID, name)

	if tag != "" {
		imageName += ":" + tag
	}

	err = cli.ImageTag(ctx, o.Source.Wanted(), imageName)
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

	reader, err := cli.ImagePush(ctx, imageName, dockertypes.ImagePushOptions{
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

	insp, _, err := cli.ImageInspectWithRaw(ctx, imageName)
	if err != nil {
		return err
	}

	o.Digest.SetCurrent(strings.Split(insp.RepoDigests[0], "@")[1])

	return reader.Close()
}

func (o *Image) delete(ctx context.Context, meta interface{}, deleteTag, deleteDigest bool) error {
	if !deleteDigest && !deleteTag {
		return nil
	}

	pctx := meta.(*config.PluginContext)

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return fmt.Errorf("error getting google credentials token: %w", err)
	}

	imageName := o.imageName(o.GCR.Current(), o.ProjectID.Current(), o.Name.Current())

	gcrrepo, err := gcrname.NewRepository(imageName)
	if err != nil {
		return nil
	}

	auth := &gcrauthn.Bearer{
		Token: token.AccessToken,
	}

	if deleteTag {
		tag := o.Tag.Current()
		if tag == "" {
			tag = "latest"
		}

		tagRef := gcrrepo.Tag(tag)

		err = gcrremote.Delete(tagRef, gcrremote.WithAuth(auth), gcrremote.WithContext(ctx))
		if err != nil {
			return err
		}
	}

	if deleteDigest {
		digestRef := gcrrepo.Digest(o.Digest.Current())

		err = gcrremote.Delete(digestRef, gcrremote.WithAuth(auth), gcrremote.WithContext(ctx))
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *Image) Delete(ctx context.Context, meta interface{}) error {
	return o.delete(ctx, meta, true, true)
}
