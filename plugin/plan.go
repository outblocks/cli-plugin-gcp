package plugin

import (
	"context"

	plugin_go "github.com/outblocks/outblocks-plugin-go"
)

func (p *Plugin) Plan(ctx context.Context, r *plugin_go.PlanRequest) (plugin_go.Response, error) {
	p.log.Errorln("plan", r.Apps, r.Dependencies)

	// res, project := validate.ValidateString(r.Properties, "project", "GCP project is required")
	// if res != nil {
	// 	return res, nil
	// }

	// res, region := validate.ValidateString(r.Properties, "region", "GCP region is required")
	// if res != nil {
	// 	return res, nil
	// }

	// p.Settings.Project = project
	// p.Settings.Region = region

	return &plugin_go.EmptyResponse{}, nil
}
