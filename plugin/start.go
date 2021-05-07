package plugin

import (
	"context"

	comm "github.com/outblocks/outblocks-plugin-go"
	"github.com/outblocks/outblocks-plugin-go/validate"
)

func (p *Plugin) Start(ctx context.Context, r *comm.StartRequest) (comm.Response, error) {
	p.log.Errorln("dupa", r.Properties, p.env.PluginDir(), p.env.ProjectPath())

	res, project := validate.ValidateString(r.Properties, "project", "GCP project is required")
	if res != nil {
		return res, nil
	}

	res, region := validate.ValidateString(r.Properties, "region", "GCP region is required")
	if res != nil {
		return res, nil
	}

	p.Settings.Project = project
	p.Settings.Region = region

	return &comm.EmptyResponse{}, nil
}
