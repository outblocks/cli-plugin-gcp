package plugin

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/validate"
	"google.golang.org/api/compute/v1"
)

func (p *Plugin) Start(ctx context.Context, r *plugin_go.StartRequest) (plugin_go.Response, error) {
	res, project := validate.String(r.Properties, "project", "GCP project is required")
	if res != nil {
		return res, nil
	}

	res, region := validate.String(r.Properties, "region", "GCP region is required")
	if res != nil {
		return res, nil
	}

	p.Settings.ProjectID = project
	p.Settings.Region = region

	cred, err := config.GoogleCredentials(ctx, compute.CloudPlatformScope)
	if err != nil {
		return nil, err
	}

	p.gcred = cred

	crmCli, err := config.NewGCPCloudResourceManager(ctx, p.gcred)
	if err != nil {
		return nil, fmt.Errorf("error creating cloud resource manager client: %w", err)
	}

	proj, err := crmCli.Projects.Get(p.Settings.ProjectID).Do()
	if err != nil {
		return nil, err
	}

	p.Settings.ProjectNumber = proj.ProjectNumber

	return &plugin_go.EmptyResponse{}, nil
}
