package actions

import (
	"context"

	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/types"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

type ApplyAction struct {
	ctx        context.Context
	storageCli *storage.Client
	computeCli *compute.Service
	dockerCli  *dockerclient.Client
	gcred      *google.Credentials
	settings   *Settings
	log        log.Logger
	env        env.Enver

	common *deploy.Common

	PluginMap        types.PluginStateMap
	AppStates        map[string]*types.AppState
	DependencyStates map[string]*types.DependencyState
}

func NewApply(ctx context.Context, gcred *google.Credentials, settings *Settings, logger log.Logger, enver env.Enver, state types.PluginStateMap, appStates map[string]*types.AppState, depStates map[string]*types.DependencyState) (*ApplyAction, error) {
	if state == nil {
		state = make(types.PluginStateMap)
	}

	if appStates == nil {
		appStates = make(map[string]*types.AppState)
	}

	if depStates == nil {
		depStates = make(map[string]*types.DependencyState)
	}

	storageCli, err := config.NewStorageClient(ctx, gcred)
	if err != nil {
		return nil, err
	}

	dockerCli, err := config.NewDockerClient()
	if err != nil {
		return nil, err
	}

	common := deploy.NewCommon()

	computeCli, err := config.NewGCPComputeClient(ctx, gcred)
	if err != nil {
		return nil, err
	}

	err = common.Decode(state[PluginCommonState])
	if err != nil {
		return nil, err
	}

	return &ApplyAction{
		ctx:        ctx,
		storageCli: storageCli,
		computeCli: computeCli,
		dockerCli:  dockerCli,
		gcred:      gcred,
		settings:   settings,
		log:        logger,
		env:        enver,

		common: common,

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
	}, nil
}

func (p *ApplyAction) handleStaticAppDeploy(state *types.AppState, app *types.AppPlanActions, callback func(obj, desc string, progress, total int)) (*deploy.StaticApp, error) {
	deployState := deploy.NewStaticApp()

	if err := deployState.Decode(state.DeployState[PluginName]); err != nil {
		return nil, err
	}

	err := deployState.Apply(p.ctx, p.storageCli, p.dockerCli, p.gcred, app.Actions, callback)
	if err != nil {
		return nil, err
	}

	return deployState, nil
}

func (p *ApplyAction) handleDeployApps(apps []*types.AppPlanActions, callback func(*types.ApplyAction)) error {
	for _, app := range apps {
		cb := func(obj, desc string, progress, total int) {
			callback(&types.ApplyAction{
				TargetID:    app.App.ID,
				TargetName:  app.App.Name,
				TargetType:  types.TargetTypeApp,
				Object:      obj,
				Description: desc,
				Progress:    progress,
				Total:       total,
			})
		}

		state := p.AppStates[app.App.ID]
		if state == nil {
			state = types.NewAppState()
			p.AppStates[app.App.ID] = state
		}

		if app.App.Type == TypeStatic {
			deployState, err := p.handleStaticAppDeploy(state, app, cb)
			if err != nil {
				return err
			}

			state.DeployState[PluginName] = deployState
		}

		state.App = app.App
	}

	return nil
}

func (p *ApplyAction) ApplyDeploy(plan *types.Plan, callback func(*types.ApplyAction)) error {
	err := p.handleDeployApps(plan.Apps, callback)
	if err != nil {
		return err
	}

	for _, actions := range plan.Plugin {
		cb := func(obj, desc string, progress, total int) {
			callback(&types.ApplyAction{
				TargetID:    PluginName,
				TargetType:  types.TargetTypePlugin,
				Object:      obj,
				Description: desc,
				Progress:    progress,
				Total:       total,
			})
		}

		if actions.Object == PluginCommonState {
			err = p.common.Apply(p.ctx, p.computeCli, actions.Actions, cb)
			if err != nil {
				return err
			}

			p.PluginMap[PluginCommonState] = p.common
		}
	}

	return nil
}

func (p *ApplyAction) ApplyDNS(plan *types.Plan, callback func(*types.ApplyAction)) error {
	return nil
}
