package actions

import (
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *PlanAction) planServiceAppDeploy(appPlan *types.AppPlan) (*deploy.ServiceApp, error) {
	appDeploy := deploy.NewServiceApp(appPlan.App)
	pctx := p.pluginCtx

	err := appDeploy.Plan(pctx, p.registry, &deploy.ServiceAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Env:       p.env,
	}, p.verify)

	return appDeploy, err
}

func (p *PlanAction) planServiceAppsDeploy(appPlans []*types.AppPlan) (ret map[string]*deploy.ServiceApp, err error) {
	ret = make(map[string]*deploy.ServiceApp, len(appPlans))

	for _, plan := range appPlans {
		app, err := p.planServiceAppDeploy(plan)
		if err != nil {
			return ret, err
		}

		ret[app.App.ID] = app
	}

	return ret, nil
}
