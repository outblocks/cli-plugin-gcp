package actions

import (
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/types"
)

type ApplyAction struct {
	pluginCtx *config.PluginContext
	log       log.Logger

	PluginMap        types.PluginStateMap
	AppStates        map[string]*types.AppState
	DependencyStates map[string]*types.DependencyState
}

func NewApply(pctx *config.PluginContext, logger log.Logger, state types.PluginStateMap, appStates map[string]*types.AppState, depStates map[string]*types.DependencyState) (*ApplyAction, error) {
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
		pluginCtx: pctx,
		log:       logger,

		PluginMap:        state,
		AppStates:        appStates,
		DependencyStates: depStates,
	}, nil
}

func (p *ApplyAction) handleStaticAppDeploy(state *types.AppState, app *types.AppPlanActions, callback func(obj, desc string, idx, progress, total int)) (*deploy.StaticApp, error) {
	deployState := deploy.NewStaticApp()
	if err := deployState.Decode(state.DeployState[PluginName]); err != nil {
		return nil, err
	}

	err := app.Apply(p.pluginCtx, deployState, callback)
	if err != nil {
		return nil, err
	}

	return deployState, nil
}

func (p *ApplyAction) handleDeployApps(apps []*types.AppPlanActions, callback func(*types.ApplyAction)) error {
	for _, app := range apps {
		cb := func(obj, desc string, idx, progress, total int) {
			callback(&types.ApplyAction{
				TargetID:    app.App.ID,
				TargetName:  app.App.Name,
				TargetType:  types.TargetTypeApp,
				Index:       idx,
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

	// Apply plugin targets.
	for _, pluginPlan := range plan.Plugin {
		cb := func(obj, desc string, idx, progress, total int) {
			callback(&types.ApplyAction{
				TargetID:    PluginName,
				TargetType:  types.TargetTypePlugin,
				Index:       idx,
				Object:      obj,
				Description: desc,
				Progress:    progress,
				Total:       total,
			})
		}

		if pluginPlan.Object == PluginLBState {
			lb := deploy.NewLoadBalancer()

			err := lb.Decode(p.PluginMap[PluginLBState])
			if err != nil {
				return err
			}

			err = pluginPlan.Apply(p.pluginCtx, lb, cb)
			if err != nil {
				return err
			}

			p.PluginMap[PluginLBState] = lb
		}
	}

	return nil
}

func (p *ApplyAction) ApplyDNS(plan *types.Plan, callback func(*types.ApplyAction)) error {
	return nil
}
