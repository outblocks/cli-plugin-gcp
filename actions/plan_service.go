package actions

import (
	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *PlanAction) planServiceAppDeploy(appPlan *types.AppPlan) (*deploy.ServiceApp, error) {
	appDeploy, err := deploy.NewServiceApp(appPlan)
	if err != nil {
		return nil, err
	}

	pctx := p.pluginCtx

	depVars, err := p.findDependenciesEnvVars(&appPlan.App.App)
	if err != nil {
		return nil, err
	}

	var databases []*deploy.DatabaseDep

	for _, need := range appPlan.App.Needs {
		if dep, ok := p.databaseDeps[need.Dependency]; ok {
			databases = append(databases, dep)
		}
	}

	err = appDeploy.Plan(pctx, p.registry, &deploy.ServiceAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Env:       appPlan.App.Env,
		Vars:      types.VarsForApp(p.appEnvVars, &appPlan.App.App, depVars),
		Databases: databases,
	})
	if err != nil {
		return nil, err
	}

	p.appDeployIDMap[appPlan.App.ID] = appDeploy

	return appDeploy, nil
}

func (p *PlanAction) planServiceAppsDeploy(appPlans []*types.AppPlan) (ret map[string]*deploy.ServiceApp, err error) {
	ret = make(map[string]*deploy.ServiceApp, len(appPlans))

	for _, plan := range appPlans {
		app, err := p.planServiceAppDeploy(plan)
		if err != nil {
			return ret, err
		}

		ret[plan.App.ID] = app
	}

	return ret, nil
}
