package plugin

import (
	"context"

	plugin_go "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/validate"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/storage/v1"
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

	cred, err := google.FindDefaultCredentials(ctx, storage.DevstorageFullControlScope)
	if err != nil {
		return nil, err
	}

	p.cred = cred

	return &plugin_go.EmptyResponse{}, nil
}
