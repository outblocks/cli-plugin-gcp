package plugin

import (
	"context"

	plugin_go "github.com/outblocks/outblocks-plugin-go"
)

func (p *Plugin) ApplyInteractive(ctx context.Context, r *plugin_go.ApplyRequest, in <-chan plugin_go.Request, out chan<- plugin_go.Response) error {
	p.log.Errorln("apply", r.Apps, r.Dependencies)

	close(out)

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

	return nil
}
