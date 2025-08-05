package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/image"
	dockerregistry "github.com/docker/docker/api/types/registry"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"google.golang.org/api/artifactregistry/v1"
)

type Image struct {
	registry.ResourceBase

	Name       fields.StringInputField
	Tag        fields.StringInputField
	ProjectID  fields.StringInputField `state:"force_new"`
	Region     fields.StringInputField `state:"force_new"`
	Digest     fields.StringOutputField
	Source     fields.StringInputField
	SourceHash fields.StringInputField

	Pull     bool `state:"-"`
	PullAuth bool `state:"-"`
}

func (o *Image) ReferenceID() string {
	return fields.GenerateID("image/%s/%s/%s", o.Region, o.ProjectID, o.Name)
}

func (o *Image) GetName() string {
	tag := fields.VerboseString(o.Tag)
	name := fields.VerboseString(o.Name)

	if tag != "" {
		name = fmt.Sprintf("%s:%s", name, tag)
	}

	return name
}

func (o *Image) imageName(region, projectID, name string) string {
	return fmt.Sprintf("%s-docker.pkg.dev/%s/%s", region, projectID, name)
}

func (o *Image) ImageName() fields.StringInputField {
	return fields.Sprintf("%s-docker.pkg.dev/%s/%s@%s", o.Region, o.ProjectID, o.Name, o.Digest)
}

func (o *Image) readImage(ctx context.Context, meta any) (*string, error) {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	region := o.Region.Any()
	projectID := o.ProjectID.Any()
	name := o.Name.Any()
	tag := o.Tag.Any()

	cli, err := pctx.GCPArtifactRegistryClient(ctx)
	if err != nil {
		return nil, err
	}

	repo := ""

	nameSplit := strings.Split(name, "/")
	if len(nameSplit) > 1 {
		repo = nameSplit[0]
		name = nameSplit[1]
	}

	if tag == "" {
		tag = "latest"
	}

	tagData, err := cli.Projects.Locations.Repositories.Packages.Tags.Get(fmt.Sprintf("projects/%s/locations/%s/repositories/%s/packages/%s/tags/%s", projectID, region, repo, name, tag)).Do()
	if err != nil {
		return nil, err
	}

	digest := tagData.Version
	digest = strings.Split(digest, "versions/")[1]

	return &digest, nil
}

func (o *Image) Read(ctx context.Context, meta any) error {
	digest, err := o.readImage(ctx, meta)
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
	o.Digest.SetCurrent(*digest)
	o.Region.SetCurrent(o.Region.Any())
	o.ProjectID.SetCurrent(o.ProjectID.Any())
	o.Source.SetCurrent(o.Source.Any())

	return nil
}

func (o *Image) BeforeDiff(context.Context, any) error {
	if o.Source.IsChanged() || o.SourceHash.IsChanged() {
		o.Digest.Invalidate()
	}

	return nil
}

func (o *Image) Create(ctx context.Context, meta any) error {
	return o.push(ctx, meta)
}

func (o *Image) Update(ctx context.Context, meta any) error {
	oldDigest := o.Digest.Current()

	err := o.push(ctx, meta)
	if err != nil {
		return err
	}

	if o.Tag.IsChanged() || oldDigest != o.Digest.Current() {
		_ = o.delete(ctx, meta, o.Tag.IsChanged() && o.Tag.Current() != "", oldDigest)
	}

	return nil
}

func (o *Image) push(ctx context.Context, meta any) error {
	pctx := meta.(*config.PluginContext) //nolint:errcheck

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return fmt.Errorf("error getting google credentials token: %w", err)
	}

	authConfig := dockerregistry.AuthConfig{
		Username: "oauth2accesstoken",
		Password: token.AccessToken,
	}

	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return err
	}

	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	dockerCli, err := pctx.DockerClient()
	if err != nil {
		return err
	}

	_, err = dockerCli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker is required for GCR image upload!\n%w", err)
	}

	if o.Pull {
		// Pull image from source.
		pullOpts := image.PullOptions{}
		if o.PullAuth {
			pullOpts.RegistryAuth = authStr
		}

		reader, err := dockerCli.ImagePull(ctx, o.Source.Wanted(), pullOpts)
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

	region := o.Region.Wanted()
	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()
	tag := o.Tag.Wanted()
	imageName := o.imageName(region, projectID, name)

	if tag != "" {
		imageName += ":" + tag
	}

	repo := ""

	nameSplit := strings.Split(name, "/")
	if len(nameSplit) > 1 {
		repo = nameSplit[0]
		name = nameSplit[1]
	}

	err = dockerCli.ImageTag(ctx, o.Source.Wanted(), imageName)
	if err != nil {
		return err
	}

	cli, err := pctx.GCPArtifactRegistryClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.Projects.Locations.Repositories.DockerImages.Get(fmt.Sprintf("projects/%s/locations/%s/repositories/%s/packages/%s", projectID, region, repo, name)).Do()
	if err != nil {
		if !ErrIs404(err) {
			return err
		}

		op, err := cli.Projects.Locations.Repositories.Create(fmt.Sprintf("projects/%s/locations/%s", projectID, region), &artifactregistry.Repository{
			Description: "Created by Outblocks",
			Format:      "DOCKER",
			Name:        fmt.Sprintf("projects/%s/locations/%s/repositories/%s", projectID, region, repo),
		}).RepositoryId(repo).Do()
		if err != nil && !ErrIs409(err) {
			return fmt.Errorf("error creating repository: %w", err)
		}

		if op != nil {
			err = WaitForArtifactRegistryOperation(ctx, cli, op)
			if err != nil {
				return fmt.Errorf("error waiting for repository creation: %w", err)
			}
		}
	}

	var insp image.InspectResponse

	for range 3 {
		reader, err := dockerCli.ImagePush(ctx, imageName, image.PushOptions{
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

		insp, err = dockerCli.ImageInspect(ctx, imageName)
		if err != nil {
			return err
		}

		if len(insp.RepoDigests) > 0 {
			break
		}

		time.Sleep(3 * time.Second)
	}

	if len(insp.RepoDigests) == 0 {
		return fmt.Errorf("error getting image digest")
	}

	o.Digest.SetCurrent(strings.Split(insp.RepoDigests[0], "@")[1])

	return nil
}

func (o *Image) delete(ctx context.Context, meta any, deleteTag bool, digest string) error {
	if digest == "" && !deleteTag {
		return nil
	}

	if o.Region.Current() == "" {
		return nil
	}

	pctx := meta.(*config.PluginContext) //nolint:errcheck

	cli, err := pctx.GCPArtifactRegistryClient(ctx)
	if err != nil {
		return err
	}

	region := o.Region.Current()
	projectID := o.ProjectID.Current()
	name := o.Name.Current()
	repo := ""

	nameSplit := strings.Split(name, "/")
	if len(nameSplit) > 1 {
		repo = nameSplit[0]
		name = nameSplit[1]
	}

	if name == "" {
		return nil
	}

	if deleteTag {
		tag := o.Tag.Current()
		if tag == "" {
			tag = "latest"
		}

		_, err := cli.Projects.Locations.Repositories.Packages.Tags.Delete(fmt.Sprintf("projects/%s/locations/%s/repositories/%s/packages/%s/tags/%s", projectID, region, repo, name, tag)).Do()
		if err != nil {
			return err
		}
	}

	if digest != "" {
		op, err := cli.Projects.Locations.Repositories.Packages.Versions.Delete(fmt.Sprintf("projects/%s/locations/%s/repositories/%s/packages/%s/versions/%s", projectID, region, repo, name, digest)).Do()
		if err != nil {
			return err
		}

		return WaitForArtifactRegistryOperation(ctx, cli, op)
	}

	return nil
}

func (o *Image) Delete(ctx context.Context, meta any) error {
	return o.delete(ctx, meta, true, o.Digest.Current())
}
