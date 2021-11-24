package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
)

func (p *Plugin) promptProject(ctx context.Context, crmCli *cloudresourcemanager.Service) (string, error) {
	res, err := p.hostCli.PromptConfirmation(ctx, &apiv1.PromptConfirmationRequest{
		Message: "Do you want to create a new GCP Project?",
	})
	if err != nil {
		return "", err
	}

	create := res.Confirmed

	if create {
		res, err := p.hostCli.PromptInput(ctx, &apiv1.PromptInputRequest{
			Message: "GCP Project name to create:",
		})
		if err != nil {
			return "", err
		}

		project := res.Answer

		op, err := crmCli.Projects.Create(&cloudresourcemanager.Project{
			Name:      project,
			ProjectId: project,
		}).Do()
		if err != nil {
			return "", fmt.Errorf("unable to create GCP project: %w", err)
		}

		err = gcp.WaitForCloudResourceManagerOperation(crmCli, op)
		if err != nil {
			return "", fmt.Errorf("unable to create GCP project: %w", err)
		}

		return project, nil
	}

	var (
		projOptions []string
		answer      string
	)

	projRes, err := crmCli.Projects.List().Do()
	if projRes != nil && len(projRes.Projects) > 0 {
		for _, proj := range projRes.Projects {
			projOptions = append(projOptions, fmt.Sprintf("%s (%s)", proj.ProjectId, proj.Name))
		}

		res, err := p.hostCli.PromptSelect(ctx, &apiv1.PromptSelectRequest{
			Message: "GCP Project to use:",
			Options: projOptions,
		})
		if err != nil {
			return "", err
		}

		answer = res.Answer
	} else {
		if err == nil {
			p.log.Warnln("Cannot find any existing projects! Specify GCP project manually.")
		} else {
			p.log.Warnln("Cannot list existing projects! Make sure you have 'gcloud' set up and authorized. You still can specify GCP project manually.")
		}

		res, err := p.hostCli.PromptInput(ctx, &apiv1.PromptInputRequest{
			Message: "GCP Project to use:",
		})
		if err != nil {
			return "", err
		}

		answer = res.Answer
	}

	return strings.SplitN(answer, " ", 2)[0], nil
}

func (p *Plugin) ProjectInit(ctx context.Context, r *apiv1.ProjectInitRequest) (*apiv1.ProjectInitResponse, error) {
	var project, region string

	cred, err := config.GoogleCredentials(ctx, compute.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("error getting google credentials, did you install and set up 'gcloud'?")
	}

	p.gcred = cred

	crmCli, err := config.NewGCPCloudResourceManager(ctx, p.gcred)
	if err != nil {
		return nil, fmt.Errorf("error creating cloud resource manager client: %w", err)
	}

	if v, ok := r.Args.Fields["project"]; ok && v.GetStringValue() != "" {
		// Check project from args.
		project = v.GetStringValue()

		_, err = crmCli.Projects.Get(project).Do()
		if gcp.ErrIs404(err) {
			return nil, fmt.Errorf("project '%s' not found or caller lacks permissions", project)
		} else if err != nil {
			return nil, fmt.Errorf("error getting project '%s': %w", project, err)
		}
	} else {
		// Prompt for project.
		project, err = p.promptProject(ctx, crmCli)
		if err != nil {
			return nil, err
		}
	}

	if v, ok := r.Args.Fields["region"]; ok && v.GetStringValue() != "" {
		// Check region from args.
		region = strings.ToLower(v.GetStringValue())

		if !plugin_util.StringSliceContains(gcp.ValidRegions, region) {
			return nil, fmt.Errorf("'%s' is not a valid region", region)
		}
	} else {
		// Prompt for region.
		res, err := p.hostCli.PromptSelect(ctx, &apiv1.PromptSelectRequest{
			Message: "GCP Region to use:",
			Default: "europe-west1",
			Options: gcp.ValidRegions,
		})
		if err != nil {
			return nil, err
		}

		region = res.Answer
	}

	props := plugin_util.MustNewStruct(map[string]interface{}{
		"project": project,
		"region":  region,
	})

	return &apiv1.ProjectInitResponse{
		Properties: props,
	}, err
}
