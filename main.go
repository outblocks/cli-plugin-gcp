package main

import (
	"github.com/outblocks/cli-plugin-gcp/plugin"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
	// "google.golang.org/api/cloudresourcemanager/v1"
)

func main() {
	// ctx := context.Background()

	// svc, err := cloudresourcemanager.NewService(ctx)
	// if err != nil {
	// 	fmt.Println(err)
	// }

	// _, err = svc.Projects.List().Do()
	// if err != nil {
	// 	fmt.Println(err)
	// }

	// for _, pp := range projects.Projects {
	// 	fmt.Println(pp.Name, pp.ProjectId)
	// }

	s := plugin_go.NewServer()
	p := plugin.NewPlugin(s.Log(), s.Env())

	err := s.Start(p.Handler())
	if err != nil {
		s.Log().Errorln(err)
	}
}
