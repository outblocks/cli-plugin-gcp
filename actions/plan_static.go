package actions

import (
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *PlanAction) planStaticAppDeploy(appPlan *types.AppPlan) (*deploy.StaticApp, error) {
	appDeploy, err := deploy.NewStaticApp(appPlan.App)
	if err != nil {
		return nil, err
	}

	pctx := p.pluginCtx

	err = appDeploy.Plan(pctx, p.registry, &deploy.StaticAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
	})

	return appDeploy, err
}

func (p *PlanAction) planStaticAppsDeploy(appPlans []*types.AppPlan) (ret map[string]*deploy.StaticApp, err error) {
	ret = make(map[string]*deploy.StaticApp, len(appPlans))

	for _, plan := range appPlans {
		app, err := p.planStaticAppDeploy(plan)
		if err != nil {
			return ret, err
		}

		ret[plan.App.ID] = app
	}

	return ret, nil
}
