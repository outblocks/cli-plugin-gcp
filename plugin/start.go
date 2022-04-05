package plugin

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/env"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/log"
	"github.com/outblocks/outblocks-plugin-go/validate"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
)

var errCredentialsMissing = fmt.Errorf(`error getting google credentials!
Supported credentials through environment variables: 'GOOGLE_APPLICATION_CREDENTIALS' pointing to a file or 'GCLOUD_SERVICE_KEY' with file contents.
Alternatively install 'gcloud' and authorize with your account: 'gcloud application-default login'`)

func (p *Plugin) Init(ctx context.Context, e env.Enver, l log.Logger, cli apiv1.HostServiceClient) error {
	p.env = e
	p.hostCli = cli
	p.log = l

	return nil
}

func (p *Plugin) Start(ctx context.Context, r *apiv1.StartRequest) (*apiv1.StartResponse, error) {
	project, err := validate.String(r.Properties.Fields, "project", "GCP 'project' is required")
	if err != nil {
		return nil, err
	}

	region, err := validate.String(r.Properties.Fields, "region", "GCP 'region' is required")
	if err != nil {
		return nil, err
	}

	p.Settings.ProjectID = project
	p.Settings.Region = region

	cred, err := config.GoogleCredentials(ctx, compute.CloudPlatformScope)
	if err != nil {
		return nil, errCredentialsMissing
	}

	p.gcred = cred

	crmCli, err := config.NewGCPCloudResourceManagerClient(ctx, p.gcred)
	if err != nil {
		return nil, fmt.Errorf("error creating gcp cloud resource manager client: %w", err)
	}

	proj, err := crmCli.Projects.Get(p.Settings.ProjectID).Do()
	if gcp.ErrIs404(err) || gcp.ErrIs403(err) {
		p.log.Warnf("Project '%s' not found or caller lacks permission!\n", p.Settings.ProjectID)

		crmCli, err := config.NewGCPCloudResourceManagerClient(ctx, p.gcred)
		if err != nil {
			return nil, fmt.Errorf("error creating gcp cloud resource manager client: %w", err)
		}

		res, err := p.hostCli.PromptConfirmation(ctx, &apiv1.PromptConfirmationRequest{
			Message: "Do you want to create a new GCP Project?",
		})
		if err != nil {
			return nil, err
		}

		create := res.Confirmed
		if !create {
			return nil, fmt.Errorf("unable to proceed without access to a GCP project")
		}

		op, err := crmCli.Projects.Create(&cloudresourcemanager.Project{
			Name:      project,
			ProjectId: project,
		}).Do()
		if err != nil {
			return nil, fmt.Errorf("unable to create GCP project: %w", err)
		}

		err = gcp.WaitForCloudResourceManagerOperation(crmCli, op)
		if err != nil {
			return nil, fmt.Errorf("unable to create GCP project: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("error getting project '%s': %w", p.Settings.ProjectID, err)
	}

	p.Settings.ProjectNumber = proj.ProjectNumber

	return &apiv1.StartResponse{}, nil
}
