package actions

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *PlanAction) planFunctionAppDeploy(ctx context.Context, appDeploy *deploy.FunctionApp, appPlan *apiv1.AppPlan, apply bool) (*deploy.FunctionApp, error) {
	pctx := p.pluginCtx

	depVars, err := p.findDependenciesEnvVars(appPlan.State.App)
	if err != nil {
		return nil, err
	}

	var databases []*deploy.DatabaseDep

	for _, need := range appPlan.State.App.Needs {
		if dep, ok := p.databaseDeps[need.Dependency]; ok {
			databases = append(databases, dep)
		}
	}

	err = appDeploy.Plan(ctx, pctx, p.registry, &deploy.FunctionAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Env:       appPlan.State.App.Env,
		Vars:      types.VarsForApp(p.appEnvVars, appPlan.State.App, depVars),
		Databases: databases,
	}, apply)
	if err != nil {
		return nil, err
	}

	p.appDeployIDMap[appPlan.State.App.Id] = appDeploy

	return appDeploy, nil
}

func (p *PlanAction) prepareFunctionAppsDeploy(appPlans []*apiv1.AppPlan) (ret []*deploy.FunctionApp, err error) {
	ret = make([]*deploy.FunctionApp, len(appPlans))
	settings := p.pluginCtx.Settings()

	for i, plan := range appPlans {
		appDeploy, err := deploy.NewFunctionApp(plan)
		if err != nil {
			return nil, err
		}

		vars := p.appEnvVars.ForApp(appDeploy.App)

		vars["cloud_url"] = fmt.Sprintf("https://%s-%s.cloudfunctions.net/%s ", settings.Region, settings.ProjectID, appDeploy.ID(p.pluginCtx))

		if appDeploy.App.Url == "" {
			vars["url"] = vars["cloud_url"]
		}

		ret[i] = appDeploy
	}

	return ret, nil
}

func (p *PlanAction) planFunctionAppsDeploy(ctx context.Context, apps []*deploy.FunctionApp, appPlans []*apiv1.AppPlan, apply bool) (ret map[string]*deploy.FunctionApp, err error) {
	ret = make(map[string]*deploy.FunctionApp, len(apps))

	for i, plan := range appPlans {
		app, err := p.planFunctionAppDeploy(ctx, apps[i], plan, apply)
		if err != nil {
			return nil, err
		}

		ret[plan.State.App.Id] = app
	}

	return ret, nil
}
