package main

import (
	"github.com/outblocks/cli-plugin-gcp/plugin"
	plugin_go "github.com/outblocks/outblocks-plugin-go"
)

func main() {
	err := plugin_go.Serve(plugin.NewPlugin(), plugin_go.WithRegistryAllowDuplicates(true))
	if err != nil {
		panic(err)
	}
}
