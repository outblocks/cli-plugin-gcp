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
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
)

const ImageName = "gcr"

type Image struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`
	GCR       string `json:"gcr"`
}

func (o *Image) ImageName() string {
	if o.Name == "" {
		return ""
	}

	return fmt.Sprintf("%s/%s/%s", o.GCR, o.ProjectID, o.Name)
}

func (o *Image) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Image
		Type string `json:"type"`
	}{
		Image: *o,
		Type:  "gcp_image",
	})
}

type ImageCreate Image

func (o *ImageCreate) ImageName() string {
	return (*Image)(o).ImageName()
}

type ImagePlan Image

func (o *ImagePlan) ImageName() string {
	return (*Image)(o).ImageName()
}

func (o *ImagePlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func deleteImageOp(o *Image) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&ImagePlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createImageOp(c *ImageCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&ImagePlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
			Source:    c.Source,
			GCR:       c.GCR,
		}).Encode(),
	}
}

func (o *Image) Plan(ctx context.Context, key string, dest interface{}, verify bool) (*types.PlanAction, error) {
	var (
		ops []*types.PlanActionOperation
		c   *ImageCreate
	)

	if dest != nil {
		c = dest.(*ImageCreate)
	}

	pctx := ctx.(*config.PluginContext)

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return nil, err
	}

	// Fetch current state if needed.
	if verify {
		err := o.verify(pctx, token.AccessToken, c)
		if err != nil {
			return nil, err
		}
	}

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(key, plugin_util.DeleteDesc(ImageName, o.ImageName()),
				append(ops, deleteImageOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(key, plugin_util.AddDesc(ImageName, c.ImageName()),
			append(ops, createImageOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID {
		return types.NewPlanActionRecreate(key, plugin_util.UpdateDesc(ImageName, c.ImageName(), "forces recreate"),
			append(ops, deleteImageOp(o), createImageOp(c))), nil
	}

	return nil, nil
}

func decodeImagePlan(p *types.PlanActionOperation) (ret *ImagePlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func (o *Image) verify(pctx *config.PluginContext, token string, c *ImageCreate) error {
	imageName := o.ImageName()
	if imageName == "" && c != nil {
		imageName = c.ImageName()
	}

	if imageName == "" {
		return nil
	}

	names := strings.SplitN(c.ImageName(), ":", 2)
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
		Token: token,
	}

	_, err = gcrremote.Head(ref, gcrremote.WithAuth(auth), gcrremote.WithContext(pctx))
	if ErrIs404(err) {
		o.Name = ""

		return nil
	} else if err != nil {
		return err
	}

	if c != nil {
		o.Name = c.Name
		o.GCR = c.GCR
		o.ProjectID = c.ProjectID
		o.Source = c.Source
	}

	return nil
}

func (o *Image) applyDeletePlan(pctx *config.PluginContext, token string, plan *ImagePlan) error {
	names := strings.SplitN(plan.ImageName(), ":", 2)
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
		Token: token,
	}

	return gcrremote.Delete(ref, gcrremote.WithAuth(auth), gcrremote.WithContext(pctx))
}

func (o *Image) applyCreatePlan(pctx *config.PluginContext, token string, plan *ImagePlan) error {
	cli, err := pctx.DockerClient()
	if err != nil {
		return err
	}

	reader, err := cli.ImagePull(pctx, GCSProxyDockerImage, dockertypes.ImagePullOptions{})
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

	err = cli.ImageTag(pctx, GCSProxyDockerImage, plan.ImageName())
	if err != nil {
		return err
	}

	authConfig := dockertypes.AuthConfig{
		Username: "oauth2accesstoken",
		Password: token,
	}

	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return err
	}

	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	reader, err = cli.ImagePush(pctx, plan.ImageName(), dockertypes.ImagePushOptions{
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

	o.Name = plan.Name
	o.GCR = plan.GCR
	o.ProjectID = plan.ProjectID
	o.Source = plan.Source

	return reader.Close()
}

func (o *Image) Apply(ctx context.Context, ops []*types.PlanActionOperation, callback types.ApplyCallbackFunc) error {
	pctx := ctx.(*config.PluginContext)

	token, err := pctx.GoogleCredentials().TokenSource.Token()
	if err != nil {
		return err
	}

	// Process operations.
	for _, op := range ops {
		plan, err := decodeImagePlan(op)
		if err != nil {
			return err
		}

		switch op.Operation {
		case types.PlanOpDelete:
			// Deletion.
			err = o.applyDeletePlan(pctx, token.AccessToken, plan)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc(ImageName, o.ImageName()))

		case types.PlanOpUpdate, types.PlanOpAdd:
			// Creation.
			err = o.applyCreatePlan(pctx, token.AccessToken, plan)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc(ImageName, o.ImageName()))
		}
	}

	return nil
}
