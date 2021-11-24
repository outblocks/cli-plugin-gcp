package actions

import (
	"github.com/outblocks/cli-plugin-gcp/deploy"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *PlanAction) planServiceAppDeploy(appPlan *apiv1.AppPlan) (*deploy.ServiceApp, error) {
	appDeploy, err := deploy.NewServiceApp(appPlan)
	if err != nil {
		return nil, err
	}

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

	err = appDeploy.Plan(pctx, p.registry, &deploy.ServiceAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Env:       appPlan.State.App.Env,
		Vars:      types.VarsForApp(p.appEnvVars, appPlan.State.App, depVars),
		Databases: databases,
	})
	if err != nil {
		return nil, err
	}

	p.appDeployIDMap[appPlan.State.App.Id] = appDeploy

	return appDeploy, nil
}

func (p *PlanAction) planServiceAppsDeploy(appPlans []*apiv1.AppPlan) (ret map[string]*deploy.ServiceApp, err error) {
	ret = make(map[string]*deploy.ServiceApp, len(appPlans))

	for _, plan := range appPlans {
		app, err := p.planServiceAppDeploy(plan)
		if err != nil {
			return ret, err
		}

		ret[plan.State.App.Id] = app
	}

	return ret, nil
}
