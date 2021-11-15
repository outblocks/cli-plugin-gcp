package plugin

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/validate"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
)

func (p *Plugin) StartInteractive(ctx context.Context, r *plugin_go.StartRequest, stream *plugin_go.ReceiverStream) error {
	res, project := validate.String(r.Properties, "project", "GCP 'project' is required")
	if res != nil {
		return stream.Send(res)
	}

	res, region := validate.String(r.Properties, "region", "GCP 'region' is required")
	if res != nil {
		return stream.Send(res)
	}

	p.Settings.ProjectID = project
	p.Settings.Region = region

	cred, err := config.GoogleCredentials(ctx, compute.CloudPlatformScope)
	if err != nil {
		return fmt.Errorf("error getting google credentials, did you install and set up 'gcloud'?")
	}

	p.gcred = cred

	crmCli, err := config.NewGCPCloudResourceManager(ctx, p.gcred)
	if err != nil {
		return fmt.Errorf("error creating cloud resource manager client: %w", err)
	}

	proj, err := crmCli.Projects.Get(p.Settings.ProjectID).Do()
	if gcp.ErrIs404(err) || gcp.ErrIs403(err) {
		_ = stream.Send(&plugin_go.MessageResponse{
			Message:  fmt.Sprintf("Project '%s' not found or caller lacks permission!", p.Settings.ProjectID),
			LogLevel: plugin_go.MessageLogLevelWarn,
		})

		crmCli, err := config.NewGCPCloudResourceManager(ctx, p.gcred)
		if err != nil {
			return fmt.Errorf("error creating cloud resource manager client: %w", err)
		}

		_ = stream.Send(&plugin_go.PromptConfirmation{
			Message: "Do you want to create a new GCP Project?",
		})

		res, err := stream.Recv()
		if err != nil {
			return err
		}

		create := res.(*plugin_go.PromptConfirmationAnswer).Confirmed
		if !create {
			return fmt.Errorf("unable to proceed without access to a GCP project")
		}

		op, err := crmCli.Projects.Create(&cloudresourcemanager.Project{
			Name:      project,
			ProjectId: project,
		}).Do()
		if err != nil {
			return fmt.Errorf("unable to create GCP project: %w", err)
		}

		err = gcp.WaitForCloudResourceManagerOperation(crmCli, op)
		if err != nil {
			return fmt.Errorf("unable to create GCP project: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("error getting project '%s': %w", p.Settings.ProjectID, err)
	}

	p.Settings.ProjectNumber = proj.ProjectNumber

	return nil
}
