package main

import (
	"github.com/outblocks/cli-plugin-gcp/plugin"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
)

func main() {
	s := plugin_go.NewServer()
	p := plugin.NewPlugin(s.Log(), s.Env())

	err := s.Start(p.Handler())
	if err != nil {
		s.Log().Errorln(err)
	}
}
