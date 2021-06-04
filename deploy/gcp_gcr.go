package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	dockertypes "github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	gcrname "github.com/google/go-containerregistry/pkg/name"
	gcrremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"golang.org/x/oauth2/google"
)

type GCPImage struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	ProjectID string `json:"project_id" mapstructure:"project_id"`
	GCR       string `json:"gcr"`
}

func (o *GCPImage) ImageName() string {
	if o.Name == "" {
		return ""
	}

	return fmt.Sprintf("%s/%s/%s", o.GCR, o.ProjectID, o.Name)
}

func (o *GCPImage) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPImage
		Type string `json:"type"`
	}{
		GCPImage: *o,
		Type:     "gcp_image",
	})
}

type GCPImageCreate GCPImage

func (o *GCPImageCreate) ImageName() string {
	return (*GCPImage)(o).ImageName()
}

type GCPImagePlan GCPImage

func (o *GCPImagePlan) ImageName() string {
	return (*GCPImage)(o).ImageName()
}

func (o *GCPImagePlan) Encode() []byte {
	d, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}

	return d
}

func deleteGCPImageOp(o *GCPImage) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpDelete,
		Data: (&GCPImagePlan{
			Name:      o.Name,
			ProjectID: o.ProjectID,
		}).Encode(),
	}
}

func createGCPImageOp(c *GCPImageCreate) *types.PlanActionOperation {
	return &types.PlanActionOperation{
		Steps:     1,
		Operation: types.PlanOpAdd,
		Data: (&GCPImagePlan{
			Name:      c.Name,
			ProjectID: c.ProjectID,
			Source:    c.Source,
			GCR:       c.GCR,
		}).Encode(),
	}
}

func (o *GCPImage) Plan(ctx context.Context, cred *google.Credentials, c *GCPImageCreate, verify bool) (*types.PlanAction, error) {
	var ops []*types.PlanActionOperation

	token, err := cred.TokenSource.Token()
	if err != nil {
		return nil, err
	}

	if verify {
		err := o.verify(ctx, token.AccessToken, c)
		if err != nil {
			return nil, err
		}
	}

	// Deletions.
	if c == nil {
		if o.Name != "" {
			return types.NewPlanActionDelete(plugin_util.DeleteDesc("gcr", o.ImageName()),
				append(ops, deleteGCPImageOp(o))), nil
		}

		return nil, nil
	}

	// Check for fresh create.
	if o.Name == "" {
		return types.NewPlanActionCreate(plugin_util.AddDesc("gcr", c.ImageName()),
			append(ops, createGCPImageOp(c))), nil
	}

	// Check for conflicting updates.
	if o.ProjectID != c.ProjectID {
		return types.NewPlanActionRecreate(plugin_util.UpdateDesc("gcr", c.ImageName(), "forces recreate"),
			append(ops, deleteGCPImageOp(o), createGCPImageOp(c))), nil
	}

	return nil, nil
}

func decodeGCPImagePlan(p *types.PlanActionOperation) (ret *GCPImagePlan, err error) {
	err = json.Unmarshal(p.Data, &ret)

	return
}

func (o *GCPImage) verify(ctx context.Context, token string, c *GCPImageCreate) error {
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

	_, err = gcrremote.Head(ref, gcrremote.WithAuth(auth), gcrremote.WithContext(ctx))
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

func (o *GCPImage) applyDeletePlan(ctx context.Context, token string, plan *GCPImagePlan) error {
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

	return gcrremote.Delete(ref, gcrremote.WithAuth(auth), gcrremote.WithContext(ctx))
}

func (o *GCPImage) applyCreatePlan(ctx context.Context, cli *dockerclient.Client, token string, plan *GCPImagePlan) error {
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

	err = cli.ImageTag(ctx, GCSProxyDockerImage, plan.ImageName())
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

	reader, err = cli.ImagePush(ctx, plan.ImageName(), dockertypes.ImagePushOptions{
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

func (o *GCPImage) Apply(ctx context.Context, cli *dockerclient.Client, cred *google.Credentials, a *types.PlanAction, callback util.ApplyCallbackFunc) error {
	token, err := cred.TokenSource.Token()
	if err != nil {
		return err
	}

	// Process operations.
	for _, p := range a.Operations {
		plan, err := decodeGCPImagePlan(p)
		if err != nil {
			return err
		}

		switch p.Operation {
		case types.PlanOpDelete:
			// Deletion.
			err = o.applyDeletePlan(ctx, token.AccessToken, plan)
			if err != nil {
				return err
			}

			callback(plugin_util.DeleteDesc("gcr", o.ImageName()))

		case types.PlanOpUpdate, types.PlanOpAdd:
			// Creation.
			err = o.applyCreatePlan(ctx, cli, token.AccessToken, plan)
			if err != nil {
				return err
			}

			callback(plugin_util.AddDesc("gcr", o.ImageName()))
		}
	}

	return nil
}

func (o *GCPImage) planner(ctx context.Context, cred *google.Credentials, c *GCPImageCreate, verify bool) func() (*types.PlanAction, error) {
	return func() (*types.PlanAction, error) {
		return o.Plan(ctx, cred, c, verify)
	}
}

func (o *GCPImage) applier(ctx context.Context, cli *dockerclient.Client, cred *google.Credentials) func(*types.PlanAction, util.ApplyCallbackFunc) error {
	return func(a *types.PlanAction, cb util.ApplyCallbackFunc) error {
		return o.Apply(ctx, cli, cred, a, cb)
	}
}