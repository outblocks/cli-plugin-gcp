package actions

import (
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func (p *PlanAction) planDatabaseDepDeploy(depPlan *apiv1.DependencyPlan, needs map[*apiv1.App]*apiv1.AppNeed) (*deploy.DatabaseDep, error) {
	depDeploy, err := deploy.NewDatabaseDep(depPlan.State.Dependency)
	if err != nil {
		return nil, err
	}

	pctx := p.pluginCtx

	depNeeds := make(map[*apiv1.App]*types.DatabaseDepNeed, len(needs))

	for app, n := range needs {
		need, err := types.NewDatabaseDepNeed(n.Properties.AsMap())
		if err != nil {
			return nil, err
		}

		if need.User == "" {
			need.User = app.Id
		}

		if need.Database == "" {
			need.Database = app.Id
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

	p.depDeployIDMap[depPlan.State.Dependency.Id] = depDeploy

	return depDeploy, nil
}

func (p *PlanAction) planDatabaseDepsDeploy(depPlans []*apiv1.DependencyPlan, allNeeds map[string]map[*apiv1.App]*apiv1.AppNeed) (ret map[string]*deploy.DatabaseDep, err error) {
	ret = make(map[string]*deploy.DatabaseDep, len(depPlans))

	for _, plan := range depPlans {
		dep, err := p.planDatabaseDepDeploy(plan, allNeeds[plan.State.Dependency.Name])
		if err != nil {
			return ret, err
		}

		ret[plan.State.Dependency.Name] = dep
	}

	return ret, nil
}

func (p *PlanAction) planStorageDepDeploy(depPlan *apiv1.DependencyPlan, needs map[*apiv1.App]*apiv1.AppNeed) (*deploy.StorageDep, error) {
	depDeploy, err := deploy.NewStorageDep(depPlan.State.Dependency)
	if err != nil {
		return nil, err
	}

	pctx := p.pluginCtx

	depNeeds := make(map[*apiv1.App]*deploy.StorageDepNeed, len(needs))

	for app, n := range needs {
		need, err := deploy.NewStorageDepNeed(n.Properties.AsMap())
		if err != nil {
			return nil, err
		}

		depNeeds[app] = need
	}

	err = depDeploy.Plan(pctx, p.registry, &deploy.StorageDepArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Needs:     depNeeds,
	})
	if err != nil {
		return nil, err
	}

	p.depDeployIDMap[depPlan.State.Dependency.Id] = depDeploy

	return depDeploy, nil
}

func (p *PlanAction) planStorageDepsDeploy(depPlans []*apiv1.DependencyPlan, allNeeds map[string]map[*apiv1.App]*apiv1.AppNeed) (ret map[string]*deploy.StorageDep, err error) {
	ret = make(map[string]*deploy.StorageDep, len(depPlans))

	for _, plan := range depPlans {
		dep, err := p.planStorageDepDeploy(plan, allNeeds[plan.State.Dependency.Name])
		if err != nil {
			return ret, err
		}

		ret[plan.State.Dependency.Name] = dep
	}

	return ret, nil
}

func (p *PlanAction) findDependencyEnvVars(app *apiv1.App, need *apiv1.AppNeed) (map[string]any, error) {
	if dep, ok := p.databaseDeps[need.Dependency]; ok {
		depNeed := dep.Needs[app]

		vars := make(map[string]any)
		vars["user"] = dep.CloudSQLUsers[depNeed.User].Name
		vars["password"] = dep.CloudSQLUsers[depNeed.User].Password
		vars["database"] = dep.CloudSQLDatabases[depNeed.Database].Name
		vars["port"] = fields.Int(5432)
		vars["host"] = fields.Sprintf("/cloudsql/%s", dep.CloudSQL.ConnectionName)
		vars["socket"] = fields.Sprintf("/cloudsql/%s", dep.CloudSQL.ConnectionName)

		return vars, nil
	}

	if dep, ok := p.storageDeps[need.Dependency]; ok {
		vars := make(map[string]any)
		vars["name"] = dep.Bucket.Name
		vars["url"] = fields.Sprintf("https://storage.googleapis.com/%s", dep.Bucket.Name)
		vars["endpoint"] = "https://storage.googleapis.com/storage/v1/"

		return vars, nil
	}

	return nil, fmt.Errorf("unable to find dependency '%s'", need.Dependency)
}

func (p *PlanAction) findDependenciesEnvVars(app *apiv1.App) (map[string]any, error) {
	depVars := make(map[string]any)

	for _, need := range app.Needs {
		vars, err := p.findDependencyEnvVars(app, need)
		if err != nil {
			return nil, err
		}

		depVars[need.Dependency] = vars
	}

	return depVars, nil
}
