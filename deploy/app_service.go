package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/creasty/defaults"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
	"github.com/outblocks/outblocks-plugin-go/util/command"
)

type CloudRunSettings struct {
	Region      string `json:"region"`
	ProjectHash string `json:"project_hash"`
	RegionCode  string `json:"region_code"`
}

func (s *CloudRunSettings) URLSuffix() string {
	return fmt.Sprintf("%s-%s", s.ProjectHash, s.RegionCode)
}

type ServiceApp struct {
	Image    *gcp.Image
	CloudRun *gcp.CloudRun

	App        *apiv1.App
	Build      *apiv1.AppBuild
	Props      *types.ServiceAppProperties
	DeployOpts *ServiceAppDeployOptions
}

type ServiceAppArgs struct {
	ProjectID string
	Region    string
	Env       map[string]string
	Vars      map[string]interface{}
	Databases []*DatabaseDep
	Settings  *CloudRunSettings
}

type ServiceAppDeployOptions struct {
	SkipRunsd   bool    `mapstructure:"skip_runsd"`
	CPULimit    float64 `mapstructure:"cpu_limit" default:"1"`
	MemoryLimit int     `mapstructure:"memory_limit" default:"256"`
	MinScale    int     `mapstructure:"min_scale" default:"0"`
	MaxScale    int     `mapstructure:"max_scale" default:"100"`
}

func NewServiceAppDeployOptions(in map[string]interface{}) (*ServiceAppDeployOptions, error) {
	o := &ServiceAppDeployOptions{}

	err := mapstructure.Decode(in, o)
	if err != nil {
		return nil, fmt.Errorf("error decoding service app deploy options: %w", err)
	}

	err = defaults.Set(o)
	if err != nil {
		return nil, err
	}

	return o, validation.ValidateStruct(o,
		validation.Field(&o.CPULimit, validation.In(1.0, 2.0, 4.0)),
		validation.Field(&o.MemoryLimit, validation.Min(128), validation.Max(8192)),
		validation.Field(&o.MinScale, validation.Min(0), validation.Max(100)),
		validation.Field(&o.MaxScale, validation.Min(1)),
	)
}

func NewServiceApp(plan *apiv1.AppPlan) (*ServiceApp, error) {
	opts, err := types.NewServiceAppProperties(plan.State.App.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	deployOpts, err := NewServiceAppDeployOptions(plan.State.App.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	if plan.Build == nil {
		plan.Build = &apiv1.AppBuild{}
	}

	return &ServiceApp{
		App:        plan.State.App,
		Build:      plan.Build,
		Props:      opts,
		DeployOpts: deployOpts,
	}, nil
}

func (o *ServiceApp) ID(pctx *config.PluginContext) string {
	return gcp.ID(pctx.Env(), o.App.Id)
}

func (o *ServiceApp) addRunsd(ctx context.Context, pctx *config.PluginContext, apply bool) error {
	dockerCli, err := pctx.DockerClient()
	if err != nil {
		return err
	}

	runsdImage := o.Build.LocalDockerImage + "/runsd"

	var runsdImageSHA string

	if !apply {
		inspect, _, err := dockerCli.ImageInspectWithRaw(ctx, o.Build.LocalDockerImage)
		if err != nil {
			return err
		}

		dir, err := ioutil.TempDir("", "runsd")
		if err != nil {
			return err
		}

		defer os.RemoveAll(dir)

		entrypoint := []string{"/bin/runsd"}
		homeEnv := ""

		if inspect.Config.User != "" {
			homeEnv = fmt.Sprintf("ENV HOME=/home/%s", inspect.Config.User)
			entrypoint = append(entrypoint, "--user", inspect.Config.User)
		}

		entrypoint = append(entrypoint, "--")

		var dockerSuffix string

		if len(inspect.Config.Entrypoint) != 0 {
			entrypoint = append(entrypoint, inspect.Config.Entrypoint...)

			if len(inspect.Config.Cmd) > 0 {
				dockerSuffix = fmt.Sprintf(`CMD ["%s"]`, strings.Join(inspect.Config.Cmd, `" , "`))
			}
		} else {
			entrypoint = append(entrypoint, inspect.Config.Cmd...)
		}

		dockerfileContent := fmt.Sprintf(`
FROM %s
USER root
%s

ADD %s /bin/runsd
RUN chmod +x /bin/runsd

ENTRYPOINT ["%s"]
%s`,
			o.Build.LocalDockerImage,
			homeEnv,
			gcp.RunsdDownloadLink,
			strings.Join(entrypoint, `", "`),
			dockerSuffix,
		)

		dockerfile := filepath.Join(dir, "Dockerfile")

		err = os.WriteFile(dockerfile, []byte(dockerfileContent), 0o644)
		if err != nil {
			return err
		}

		cmd, err := command.New(
			exec.Command("docker", "build", "--platform=linux/amd64", "--tag", runsdImage, "."),
			command.WithDir(dir),
			command.WithEnv([]string{"DOCKER_BUILDKIT=1"}),
		)
		if err != nil {
			return err
		}

		done := make(chan struct{})

		var stderr []byte

		go func() {
			stderr, _ = io.ReadAll(cmd.Stderr())

			close(done)
		}()

		err = cmd.Run()
		if err != nil {
			return err
		}

		err = cmd.Wait()
		if err != nil {
			<-done

			if len(stderr) != 0 {
				return errors.New(string(stderr))
			}

			return err
		}
	}

	inspect, _, err := dockerCli.ImageInspectWithRaw(ctx, runsdImage)
	if err != nil {
		return err
	}

	runsdImageSHA = inspect.ID

	o.Image.SourceHash = fields.String(runsdImageSHA)
	o.Image.Source = fields.String(runsdImage)

	return nil
}

func (o *ServiceApp) Plan(ctx context.Context, pctx *config.PluginContext, r *registry.Registry, c *ServiceAppArgs, apply bool) error {
	// Add GCR docker image.
	o.Image = &gcp.Image{
		Name:      fields.String(gcp.ImageID(pctx.Env(), o.App.Id)),
		ProjectID: fields.String(c.ProjectID),
		GCR:       fields.String(gcp.RegionToGCR(c.Region)),
		Pull:      false,
	}

	if o.Build.LocalDockerImage != "" && o.Build.LocalDockerHash != "" {
		if !o.DeployOpts.SkipRunsd {
			if o.Props.Container.Port == 80 {
				return fmt.Errorf("cannot inject runsd to service app '%s' running at port 80 - run at different port", o.App.Name)
			}

			err := o.addRunsd(ctx, pctx, apply)
			if err != nil {
				return fmt.Errorf("adding runsd to image of service app '%s' failed: %w", o.App.Name, err)
			}
		} else {
			o.Image.SourceHash = fields.String(o.Build.LocalDockerHash)
			o.Image.Source = fields.String(o.Build.LocalDockerImage)
		}
	}

	_, err := r.RegisterAppResource(o.App, "image", o.Image)
	if err != nil {
		return err
	}

	// Expand env vars.
	cloudRunHash := "unknown"

	if c.Settings != nil {
		cloudRunHash = c.Settings.ProjectHash
	}

	if c.Env == nil {
		c.Env = make(map[string]string)
	}

	c.Env["CLOUD_RUN_PROJECT_HASH"] = cloudRunHash

	envVars := make(map[string]fields.Field, len(c.Env))
	eval := fields.NewFieldVarEvaluator(c.Vars)

	for k, v := range c.Env {
		exp, err := eval.Expand(v)
		if err != nil {
			return err
		}

		envVars[k] = exp
	}

	// Add cloud run service.
	cloudSQLconnFmt := make([]string, len(c.Databases))
	cloudSQLconnNames := make([]interface{}, len(c.Databases))

	for i, db := range c.Databases {
		cloudSQLconnFmt[i] = "%s"
		cloudSQLconnNames[i] = db.CloudSQL.ConnectionName
	}

	cmd := make([]fields.Field, len(o.Props.Container.Entrypoint.ArrayOrShell()))
	for i, v := range o.Props.Container.Entrypoint.ArrayOrShell() {
		cmd[i] = fields.String(v)
	}

	args := make([]fields.Field, len(o.Props.Container.Command.ArrayOrShell()))
	for i, v := range o.Props.Container.Command.ArrayOrShell() {
		args[i] = fields.String(v)
	}

	o.CloudRun = &gcp.CloudRun{
		Name:      fields.String(o.ID(pctx)),
		Command:   fields.Array(cmd),
		Args:      fields.Array(args),
		Port:      fields.Int(o.Props.Container.Port),
		ProjectID: fields.String(c.ProjectID),
		Region:    fields.String(c.Region),
		Image:     o.Image.ImageName(),
		IsPublic:  fields.Bool(!o.Props.Private),
		EnvVars:   fields.Map(envVars),

		CloudSQLInstances: fields.Sprintf(strings.Join(cloudSQLconnFmt, ","), cloudSQLconnNames...),
		MinScale:          fields.Int(o.DeployOpts.MinScale),
		MaxScale:          fields.Int(o.DeployOpts.MaxScale),
		MemoryLimit:       fields.String(fmt.Sprintf("%dMi", o.DeployOpts.MemoryLimit)),
		CPULimit:          fields.String(fmt.Sprintf("%dm", int(o.DeployOpts.CPULimit*1000))),
	}

	_, err = r.RegisterAppResource(o.App, "cloud_run", o.CloudRun)
	if err != nil {
		return err
	}

	return nil
}
