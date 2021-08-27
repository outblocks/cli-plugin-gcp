package actions

import (
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *PlanAction) planStaticAppDeploy(appPlan *types.AppPlan) (*deploy.StaticApp, error) {
	appDeploy := deploy.NewStaticApp(appPlan.App)
	pctx := p.pluginCtx

	err := appDeploy.Plan(pctx, p.registry, appPlan.App, &deploy.StaticAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Path:      appPlan.Path,
	}, p.verify)

	return appDeploy, err
}

func (p *PlanAction) planStaticAppsDeploy(appPlans []*types.AppPlan) (ret map[string]*deploy.StaticApp, err error) {
	ret = make(map[string]*deploy.StaticApp, len(appPlans))

	for _, plan := range appPlans {
		app, err := p.planStaticAppDeploy(plan)
		if err != nil {
			return ret, err
		}

		ret[app.App.ID] = app
	}

	return ret, nil
}
