package actions

import (
	"context"

	"cloud.google.com/go/storage"
	"github.com/outblocks/cli-plugin-gcp/statetypes/deploy"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/types"
)

type ApplyAction struct {
	ctx      context.Context
	cli      *storage.Client
	settings *Settings
	log      log.Logger
	env      env.Enver

	PluginMap        types.PluginStateMap
	AppStates        map[string]*types.AppState
	DependencyStates map[string]*types.DependencyState
}

func NewApply(ctx context.Context, settings *Settings, logger log.Logger, enver env.Enver, state types.PluginStateMap, appStates map[string]*types.AppState, depStates map[string]*types.DependencyState) *ApplyAction {
	cli, err := storage.NewClient(ctx)
	if err != nil {
		panic(err)
	}

	if state == nil {
		state = make(types.PluginStateMap)
	}

	if appStates == nil {
		appStates = make(map[string]*types.AppState)
	}

	if depStates == nil {
		depStates = make(map[string]*types.DependencyState)
	}

	return &ApplyAction{
		ctx:      ctx,
		cli:      cli,
		settings: settings,
		log:      logger,
		env:      enver,

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
	}
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

	// Apply Bucket.
	if deployState.Bucket == nil {
		deployState.Bucket = &deploy.GCPBucket{}
	}

	err := deployState.Bucket.Apply(p.ctx, p.cli, StaticBucketObject, app.Actions[StaticBucketObject], callback)
	if err != nil {
		return err
	}

	// TODO: apply cloud run and LB

	state.DeployState[PluginName] = deployState

	return nil
}

func (p *ApplyAction) handleDeployApps(apps []*types.AppPlanActions, callback func(*types.ApplyAction)) error {
	for _, app := range apps {
		p.log.Errorln("DEPLOY", app.App.Type, app.Actions)

		if app.App.Type == TypeStatic {
			for _, act := range app.Actions {
				p.log.Errorln("ACTION", act.Description, act.Operations)
			}

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
