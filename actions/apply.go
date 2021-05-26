package actions

import (
	"context"

	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/cli-plugin-gcp/statetypes/deploy"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/types"
	"golang.org/x/oauth2/google"
)

type ApplyAction struct {
	ctx        context.Context
	storageCli *storage.Client
	dockerCli  *dockerclient.Client
	gcred      *google.Credentials
	settings   *Settings
	log        log.Logger
	env        env.Enver

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

	storageCli, err := config.NewStorageCli(ctx, gcred)
	if err != nil {
		return nil, err
	}

	dockerCli, err := config.NewDockerCli()
	if err != nil {
		return nil, err
	}

	return &ApplyAction{
		ctx:        ctx,
		storageCli: storageCli,
		dockerCli:  dockerCli,
		gcred:      gcred,
		settings:   settings,
		log:        logger,
		env:        enver,

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
	}, nil
}

func (p *ApplyAction) handleStaticAppDeploy(app *types.AppPlanActions, callback func(*types.ApplyAction)) error {
	var deployState deploy.StaticApp

	state := p.AppStates[app.App.Name]
	if state == nil {
		state = types.NewAppState()
		p.AppStates[app.App.Name] = state
	}

	if err := deployState.Decode(state.DeployState[PluginName]); err != nil {
		return err
	}

	target := app.App.TargetName()

	// Apply Bucket.
	if deployState.Bucket == nil {
		deployState.Bucket = &deploy.GCPBucket{}
	}

	err := deployState.Bucket.Apply(p.ctx, p.storageCli, target, StaticBucketObject, app.Actions[StaticBucketObject], callback)
	if err != nil {
		return err
	}

	// Apply GCR.
	if deployState.ProxyImage == nil {
		deployState.ProxyImage = &deploy.GCPImage{}
	}

	err = deployState.ProxyImage.Apply(p.ctx, p.dockerCli, p.gcred, target, StaticProxyImage, app.Actions[StaticProxyImage], callback)
	if err != nil {
		return err
	}

	// TODO: apply cloud run and LB

	state.DeployState[PluginName] = deployState

	return nil
}

func (p *ApplyAction) handleDeployApps(apps []*types.AppPlanActions, callback func(*types.ApplyAction)) error {
	for _, app := range apps {
		if app.App.Type == TypeStatic {
			err := p.handleStaticAppDeploy(app, callback)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (p *ApplyAction) ApplyDeploy(plan *types.Plan, callback func(*types.ApplyAction)) error {
	err := p.handleDeployApps(plan.Apps, callback)
	if err != nil {
		return err
	}

	return nil
}

func (p *ApplyAction) ApplyDNS(plan *types.Plan, callback func(*types.ApplyAction)) error {
	return nil
}
