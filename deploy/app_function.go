package deploy

import (
	"context"
	"fmt"

	"github.com/creasty/defaults"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
)

type FunctionApp struct {
	Bucket             *gcp.Bucket
	Archive            *gcp.BucketObject
	CloudFunction      *gcp.CloudFunction
	CloudSchedulerJobs []*gcp.CloudSchedulerJob

	App        *apiv1.App
	Skip       bool
	Build      *apiv1.AppBuild
	Props      *types.FunctionAppProperties
	DeployOpts *FunctionAppDeployOptions
}

type FunctionAppArgs struct {
	ProjectID string
	Region    string
	Env       map[string]string
	Vars      map[string]interface{}
	Databases []*DatabaseDep
}

type FunctionAppDeployOptions struct {
	types.FunctionAppDeployOptions
}

func NewFunctionAppDeployOptions(in map[string]interface{}) (*FunctionAppDeployOptions, error) {
	o := &FunctionAppDeployOptions{}

	err := plugin_util.MapstructureJSONDecode(in, o)
	if err != nil {
		return nil, fmt.Errorf("error decoding function app deploy options: %w", err)
	}

	err = defaults.Set(o)
	if err != nil {
		return nil, err
	}

	// Manual defaults.
	if o.MemoryLimit == 0 {
		o.MemoryLimit = 256
	}

	if o.Timeout == 0 {
		o.Timeout = 300
	}

	return o, validation.ValidateStruct(o,
		validation.Field(&o.MemoryLimit, validation.Min(128), validation.Max(8192)),
		validation.Field(&o.MinScale, validation.Min(0)),
		validation.Field(&o.MaxScale, validation.Min(1)),
		validation.Field(&o.Timeout, validation.Min(1), validation.Max(540)),
	)
}

func NewFunctionApp(plan *apiv1.AppPlan) (*FunctionApp, error) {
	opts, err := types.NewFunctionAppProperties(plan.State.App.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	deployOpts, err := NewFunctionAppDeployOptions(plan.State.App.Properties.AsMap())
	if err != nil {
		return nil, err
	}

	if plan.Build == nil {
		plan.Build = &apiv1.AppBuild{}
	}

	return &FunctionApp{
		App:        plan.State.App,
		Skip:       plan.Skip,
		Build:      plan.Build,
		Props:      opts,
		DeployOpts: deployOpts,
	}, nil
}

func (o *FunctionApp) ID(pctx *config.PluginContext) string {
	return gcp.ID(pctx.Env(), o.App.Id)
}

func (o *FunctionApp) Plan(ctx context.Context, pctx *config.PluginContext, r *registry.Registry, c *FunctionAppArgs, apply bool) error {
	// Add bucket.
	o.Bucket = &gcp.Bucket{
		Name:       gcp.GlobalIDField(pctx.Env(), c.ProjectID, o.App.Id),
		Location:   fields.String(c.Region),
		ProjectID:  fields.String(c.ProjectID),
		Versioning: fields.Bool(false),
		Critical:   false,
	}

	_, err := r.RegisterAppResource(o.App, "bucket", o.Bucket)
	if err != nil {
		return err
	}

	o.Archive = &gcp.BucketObject{
		BucketName:  o.Bucket.Name,
		Name:        fields.String(fmt.Sprintf("%s.zip", o.Build.LocalArchiveHash)),
		Hash:        fields.String(o.Build.LocalArchiveHash),
		Path:        o.Build.LocalArchivePath,
		IsPublic:    fields.Bool(false),
		ContentType: fields.String("application/zip"),
	}

	if o.Build.LocalArchiveHash == "" {
		r.GetAppResource(o.App, "archive", o.Archive)
	}

	_, err = r.RegisterAppResource(o.App, "archive", o.Archive)
	if err != nil {
		return err
	}

	// Expand env vars.
	if c.Env == nil {
		c.Env = make(map[string]string)
	}

	envVars := make(map[string]fields.Field, len(c.Env))
	eval := fields.NewFieldVarEvaluator(c.Vars)

	for k, v := range c.Env {
		exp, err := eval.Expand(v)
		if err != nil {
			return err
		}

		envVars[k] = exp
	}

	o.CloudFunction = &gcp.CloudFunction{
		Name:         fields.String(o.ID(pctx)),
		ProjectID:    fields.String(c.ProjectID),
		Region:       fields.String(c.Region),
		Entrypoint:   fields.String(o.Props.Entrypoint),
		Runtime:      fields.String(o.Props.Runtime),
		SourceBucket: o.Bucket.Name,
		SourceObject: o.Archive.Name,
		IsPublic:     fields.Bool(!o.Props.Private),

		MinScale:       fields.Int(o.DeployOpts.MinScale),
		MaxScale:       fields.Int(o.DeployOpts.MaxScale),
		MemoryLimit:    fields.Int(o.DeployOpts.MemoryLimit),
		TimeoutSeconds: fields.Int(o.DeployOpts.Timeout),
		EnvVars:        fields.Map(envVars),
	}

	_, err = r.RegisterAppResource(o.App, "cloud_function", o.CloudFunction)
	if err != nil {
		return err
	}

	if o.App.Url != "" {
		schedulers, err := addCloudSchedulers(pctx, r, o.App, c.ProjectID, c.Region, o.Props.Scheduler)
		if err != nil {
			return err
		}

		o.CloudSchedulerJobs = schedulers
	}

	return nil
}
