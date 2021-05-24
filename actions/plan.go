package actions

import (
	"context"
	"fmt"
	"path/filepath"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/statetypes/deploy"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/types"
	"github.com/outblocks/outblocks-plugin-go/util"
)

type PlanAction struct {
	ctx      context.Context
	cli      *storage.Client
	settings *Settings
	log      log.Logger
	env      env.Enver

	pluginState types.PluginStateMap
	verify      bool
}

func NewPlan(ctx context.Context, settings *Settings, logger log.Logger, enver env.Enver, pluginState types.PluginStateMap, verify bool) *PlanAction {
	cli, err := storage.NewClient(ctx)
	if err != nil {
		panic(err)
	}

	return &PlanAction{
		ctx:         ctx,
		cli:         cli,
		settings:    settings,
		log:         logger,
		env:         enver,
		pluginState: pluginState,
		verify:      verify,
	}
}

func (p *PlanAction) handleStaticAppDeploy(app *types.App, state *types.AppState) (*types.AppPlanActions, error) {
	build := app.Properties["build"].(map[string]interface{})
	buildDir := filepath.Join(app.Path, build["dir"].(string))

	buildPath, ok := util.CheckDir(buildDir)
	if !ok {
		return nil, fmt.Errorf("app '%s' build dir '%s' does not exist", app.Name, buildDir)
	}

	p.log.Errorf("APP: name=%s, needs=%v, buildpath=%s, props=%v, typ=%s, url=%s\n", app.Name, app.Needs, buildPath, app.Properties, app.Type, app.URL)

	plan := types.NewAppPlanActions(app)

	var deployState deploy.StaticApp
	if state != nil {
		if err := deployState.Decode(state.DeployState[PluginName]); err != nil {
			return nil, err
		}
	}

	// Plan Bucket.
	if deployState.Bucket == nil {
		deployState.Bucket = &deploy.GCPBucket{}
	}

	actions, err := deployState.Bucket.Plan(p.ctx, p.cli, &deploy.GCPBucketCreate{
		Name:       deploy.BucketName(p.env.ProjectName(), p.settings.ProjectID, app.Name),
		ProjectID:  p.settings.ProjectID,
		Location:   p.settings.Region,
		Versioning: false,
		IsPublic:   true,
		Path:       buildPath,
	}, p.verify)
	if err != nil {
		return nil, err
	}

	if len(actions.Operations) != 0 {
		plan.Actions[StaticBucketObject] = actions
	}

	// TODO: plan cloud run and LB

	return plan, nil
}

func (p *PlanAction) handleStaticAppsDeploy(apps []*types.AppPlan) (appPlan []*types.AppPlanActions, err error) {
	for _, app := range apps {
		aa, e := p.handleStaticAppDeploy(app.App, app.State)
		if e != nil {
			return nil, e
		}

		if aa != nil {
			appPlan = append(appPlan, aa)
		}
	}

	return
}

func (p *PlanAction) PlanDeploy(apps []*types.AppPlan) (*types.Plan, error) {
	var (
		staticApps []*types.AppPlan
	)

	for _, app := range apps {
		if !app.IsDeploy {
			continue
		}

		if app.App.Type == TypeStatic {
			staticApps = append(staticApps, app)
		}
	}

	var (
		appPlan []*types.AppPlanActions
	)

	// Plan static app deployment.
	staticAppDeployPlan, err := p.handleStaticAppsDeploy(staticApps)
	if err != nil {
		return nil, err
	}

	appPlan = append(appPlan, staticAppDeployPlan...)

	return &types.Plan{
		Apps: appPlan,
	}, nil
}
