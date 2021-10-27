package actions

import (
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *PlanAction) planDatabaseDepDeploy(depPlan *types.DependencyPlan, needs map[*types.App]*types.AppNeed) (*deploy.DatabaseDep, error) {
	depDeploy, err := deploy.NewDatabaseDep(depPlan.Dependency)
	if err != nil {
		return nil, err
	}

	pctx := p.pluginCtx

	depNeeds := make(map[*types.App]*deploy.DatabaseDepNeed, len(needs))

	for app, n := range needs {
		need, err := deploy.NewDatabaseDepNeed(n.Properties)
		if err != nil {
			return nil, err
		}

		if need.User == "" {
			need.User = app.ID
		}

		if need.Database == "" {
			need.Database = app.ID
		}

		depNeeds[app] = need
	}

	err = depDeploy.Plan(pctx, p.registry, &deploy.DatabaseDepArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Needs:     depNeeds,
	})

	if err != nil {
		return nil, err
	}

	p.depDeployIDMap[depPlan.Dependency.ID] = depDeploy

	return depDeploy, nil
}

func (p *PlanAction) planDatabaseDepsDeploy(depPlans []*types.DependencyPlan, allNeeds map[string]map[*types.App]*types.AppNeed) (ret map[string]*deploy.DatabaseDep, err error) {
	ret = make(map[string]*deploy.DatabaseDep, len(depPlans))

	for _, plan := range depPlans {
		dep, err := p.planDatabaseDepDeploy(plan, allNeeds[plan.Dependency.Name])
		if err != nil {
			return ret, err
		}

		ret[plan.Dependency.Name] = dep
	}

	return ret, nil
}

func (p *PlanAction) findDependencyEnvVars(app *types.App, need *types.AppNeed) (map[string]interface{}, error) {
	if dep, ok := p.databaseDeps[need.Dependency]; ok {
		depNeed := dep.Needs[app]

		vars := make(map[string]interface{})
		vars["user"] = dep.CloudSQLUsers[depNeed.User].Name
		vars["password"] = dep.CloudSQLUsers[depNeed.User].Password
		vars["database"] = dep.CloudSQLDatabases[depNeed.Database].Name
		vars["port"] = fields.Int(5432)
		vars["host"] = fields.Sprintf("/cloudsql/%s", dep.CloudSQL.ConnectionName)
		vars["socket"] = fields.Sprintf("/cloudsql/%s", dep.CloudSQL.ConnectionName)

		return vars, nil
	}

	return nil, fmt.Errorf("unable to find dependency '%s'", need.Dependency)
}

func (p *PlanAction) findDependenciesEnvVars(app *types.App) (map[string]interface{}, error) {
	depVars := make(map[string]interface{})

	for _, need := range app.Needs {
		vars, err := p.findDependencyEnvVars(app, need)
		if err != nil {
			return nil, err
		}

		depVars[need.Dependency] = vars
	}

	return depVars, nil
}
