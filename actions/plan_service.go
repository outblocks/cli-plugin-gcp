package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
)

var cloudRunSuffixRegex = regexp.MustCompile(`^.+-([a-z0-9]+)-([a-z]{2})\.a\.run\.app$`)

func (p *PlanAction) planServiceAppDeploy(ctx context.Context, appDeploy *deploy.ServiceApp, appPlan *apiv1.AppPlan, apply bool) (*deploy.ServiceApp, error) {
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

	err = appDeploy.Plan(ctx, pctx, p.registry, &deploy.ServiceAppArgs{
		ProjectID: pctx.Settings().ProjectID,
		Region:    pctx.Settings().Region,
		Env:       appPlan.State.App.Env,
		Vars:      types.VarsForApp(p.appEnvVars, appPlan.State.App, depVars),
		Databases: databases,
		Settings:  p.cloudRunSettings,
	}, apply)
	if err != nil {
		return nil, err
	}

	p.appDeployIDMap[appPlan.State.App.Id] = appDeploy

	return appDeploy, nil
}

func (p *PlanAction) prepareServiceAppsDeploy(appPlans []*apiv1.AppPlan) (ret []*deploy.ServiceApp, err error) {
	ret = make([]*deploy.ServiceApp, len(appPlans))

	for i, plan := range appPlans {
		appDeploy, err := deploy.NewServiceApp(plan)
		if err != nil {
			return nil, err
		}

		vars := p.appEnvVars.ForApp(appDeploy.App)

		vars["cloud_url"] = fmt.Sprintf("https://%s-%s.a.run.app", appDeploy.ID(p.pluginCtx), p.cloudRunSettings.URLSuffix())
		vars["private_url"] = fmt.Sprintf("http://%s", appDeploy.ID(p.pluginCtx))

		if appDeploy.Props.Private {
			vars["url"] = vars["private_url"]
		}

		ret[i] = appDeploy
	}

	return ret, nil
}

func (p *PlanAction) planServiceAppsDeploy(ctx context.Context, apps []*deploy.ServiceApp, appPlans []*apiv1.AppPlan, apply bool) (ret map[string]*deploy.ServiceApp, err error) {
	ret = make(map[string]*deploy.ServiceApp, len(apps))

	for i, plan := range appPlans {
		app, err := p.planServiceAppDeploy(ctx, apps[i], plan, apply)
		if err != nil {
			return nil, err
		}

		ret[plan.State.App.Id] = app
	}

	return ret, nil
}

func (p *PlanAction) prepareCloudRunURL(ctx context.Context, create bool) error {
	var cloudRunSettings deploy.CloudRunSettings

	settings := p.State.Other["cloud_run_settings"]
	region := p.pluginCtx.Settings().Region
	project := p.pluginCtx.Settings().ProjectID

	if settings != nil {
		err := json.Unmarshal(settings, &cloudRunSettings)
		if err != nil {
			return err
		}
	}

	if cloudRunSettings.Region == region {
		p.cloudRunSettings = &cloudRunSettings

		return nil
	}

	if !create {
		return nil
	}

	p.cloudRunSettings = &cloudRunSettings

	cloudRun := &gcp.CloudRun{
		Name:      gcp.IDField(p.pluginCtx.Env(), "temporary-service"),
		Region:    fields.String(region),
		ProjectID: fields.String(project),
		Image:     fields.String("us-docker.pkg.dev/cloudrun/container/hello"),
	}

	tempReg := registry.NewRegistry(&registry.Options{})

	_, err := tempReg.RegisterPluginResource("temp", "temp", cloudRun)
	if err != nil {
		return err
	}

	p.log.Infoln("Getting GCP project's magic Cloud Run suffix...")

	err = cloudRun.Create(ctx, p.pluginCtx)
	if err != nil {
		return err
	}

	cloudRun.Wrapper().MarkAllWantedAsCurrent()
	url := cloudRun.URL.Current()

	err = cloudRun.Delete(ctx, p.pluginCtx)
	if err != nil {
		return err
	}

	urlMatch := cloudRunSuffixRegex.FindStringSubmatch(url)
	if urlMatch == nil || len(urlMatch) != 3 {
		return fmt.Errorf("cloud run URL of unknown format: %s", url)
	}

	cloudRunSettings.Region = region
	cloudRunSettings.ProjectHash = urlMatch[1]
	cloudRunSettings.RegionCode = urlMatch[2]

	data, err := json.Marshal(&cloudRunSettings)
	if err != nil {
		return err
	}

	p.State.Other["cloud_run_settings"] = data

	return nil
}
