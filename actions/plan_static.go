package actions

import (
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
)

func (p *PlanAction) planStaticAppDeploy(appDeploy *deploy.StaticApp, appPlan *apiv1.AppPlan) (*deploy.StaticApp, error) {
	pctx := p.pluginCtx

	err := appDeploy.Plan(pctx, p.registry, &deploy.StaticAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
	})

	if err != nil {
		return nil, err
	}

	p.appDeployIDMap[appPlan.State.App.Id] = appDeploy

	return appDeploy, nil
}

func (p *PlanAction) prepareStaticAppsDeploy(appPlans []*apiv1.AppPlan) (ret []*deploy.StaticApp, err error) {
	ret = make([]*deploy.StaticApp, len(appPlans))

	for i, plan := range appPlans {
		appDeploy, err := deploy.NewStaticApp(plan)
		if err != nil {
			return nil, err
		}

		vars := p.appEnvVars.ForApp(appDeploy.App)

		vars["cloud_url"] = fmt.Sprintf("https://%s-%s.a.run.app/", appDeploy.ID(p.pluginCtx), p.cloudRunSettings.URLSuffix())
		vars["private_url"] = fmt.Sprintf("http://%s/", appDeploy.ID(p.pluginCtx))

		ret[i] = appDeploy
	}

	return ret, nil
}

func (p *PlanAction) planStaticAppsDeploy(apps []*deploy.StaticApp, appPlans []*apiv1.AppPlan) (ret map[string]*deploy.StaticApp, err error) {
	ret = make(map[string]*deploy.StaticApp, len(appPlans))

	for i, plan := range appPlans {
		app, err := p.planStaticAppDeploy(apps[i], plan)
		if err != nil {
			return nil, err
		}

		ret[plan.State.App.Id] = app
	}

	return ret, nil
}
