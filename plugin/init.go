package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
)

func promptProject(stream *plugin_go.ReceiverStream, crmCli *cloudresourcemanager.Service) (string, error) {
	_ = stream.Send(&plugin_go.PromptConfirmation{
		Message: "Do you want to create a new GCP Project?",
	})

	res, err := stream.Recv()
	if err != nil {
		return "", err
	}

	create := res.(*plugin_go.PromptConfirmationAnswer).Confirmed

	if create {
		_ = stream.Send(&plugin_go.PromptInput{
			Message: "GCP Project name to create:",
		})

		res, err = stream.Recv()
		if err != nil {
			return "", err
		}

		project := res.(*plugin_go.PromptInputAnswer).Answer

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

	var projOptions []string

	projRes, err := crmCli.Projects.List().Do()
	if projRes != nil && len(projRes.Projects) > 0 {
		for _, proj := range projRes.Projects {
			projOptions = append(projOptions, fmt.Sprintf("%s (%s)", proj.ProjectId, proj.Name))
		}

		_ = stream.Send(&plugin_go.PromptSelect{
			Message: "GCP Project to use:",
			Options: projOptions,
		})
	} else {
		if err == nil {
			_ = stream.Send(&plugin_go.MessageResponse{
				Message:  "Cannot find any existing projects! Specify GCP project manually.",
				LogLevel: plugin_go.MessageLogLevelWarn,
			})
		} else {
			_ = stream.Send(&plugin_go.MessageResponse{
				Message:  "Cannot list existing projects! Make sure you have 'gcloud' set up and authorized. You still can specify GCP project manually.",
				LogLevel: plugin_go.MessageLogLevelWarn,
			})
		}

		_ = stream.Send(&plugin_go.PromptInput{
			Message: "GCP Project to use:",
		})
	}

	res, err = stream.Recv()
	if err != nil {
		return "", err
	}

	return strings.SplitN(res.(*plugin_go.PromptInputAnswer).Answer, " ", 2)[0], nil
}

func (p *Plugin) ProjectInitInteractive(ctx context.Context, r *plugin_go.ProjectInitRequest, stream *plugin_go.ReceiverStream) error {
	var project, region string

	cred, err := config.GoogleCredentials(ctx, compute.CloudPlatformScope)
	if err != nil {
		return fmt.Errorf("error getting google credentials, did you install and set up 'gcloud'?")
	}

	p.gcred = cred

	crmCli, err := config.NewGCPCloudResourceManager(ctx, p.gcred)
	if err != nil {
		return fmt.Errorf("error creating cloud resource manager client: %w", err)
	}

	if v, ok := r.Args["project"]; ok && v != "" {
		// Check project from args.
		project = v.(string)

		_, err = crmCli.Projects.Get(project).Do()
		if gcp.ErrIs404(err) {
			return fmt.Errorf("project '%s' not found or caller lacks permissions", project)
		} else if err != nil {
			return fmt.Errorf("error getting project '%s': %w", project, err)
		}
	} else {
		// Prompt for project.
		project, err = promptProject(stream, crmCli)
		if err != nil {
			return err
		}
	}

	if v, ok := r.Args["region"]; ok && v != "" {
		// Check region from args.
		region = strings.ToLower(v.(string))

		if !util.StringSliceContains(gcp.ValidRegions, region) {
			return fmt.Errorf("'%s' is not a valid region", region)
		}
	} else {
		// Prompt for region.
		_ = stream.Send(&plugin_go.PromptSelect{
			Message: "GCP Region to use:",
			Default: "europe-west1",
			Options: gcp.ValidRegions,
		})

		res, err := stream.Recv()
		if err != nil {
			return err
		}

		region = res.(*plugin_go.PromptInputAnswer).Answer
	}

	_ = stream.Send(&plugin_go.ProjectInitResponse{
		Properties: map[string]interface{}{
			"project": project,
			"region":  region,
		},
	})

	return nil
}
