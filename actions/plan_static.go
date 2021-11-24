package actions

import (
	"github.com/outblocks/cli-plugin-gcp/deploy"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
)

func (p *PlanAction) planStaticAppDeploy(appPlan *apiv1.AppPlan) (*deploy.StaticApp, error) {
	appDeploy, err := deploy.NewStaticApp(appPlan)
	if err != nil {
		return nil, err
	}

	pctx := p.pluginCtx

	err = appDeploy.Plan(pctx, p.registry, &deploy.StaticAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
	})

	if err != nil {
		return nil, err
	}

	p.appDeployIDMap[appPlan.State.App.Id] = appDeploy

	return appDeploy, nil
}

func (p *PlanAction) planStaticAppsDeploy(appPlans []*apiv1.AppPlan) (ret map[string]*deploy.StaticApp, err error) {
	ret = make(map[string]*deploy.StaticApp, len(appPlans))

	for _, plan := range appPlans {
		app, err := p.planStaticAppDeploy(plan)
		if err != nil {
			return ret, err
		}

		ret[plan.State.App.Id] = app
	}

	return ret, nil
}
