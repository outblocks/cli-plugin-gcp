package deploy

import (
	"github.com/mitchellh/mapstructure"
)

type StaticApp struct {
	Bucket       *GCPBucket       `json:"bucket"`
	CloudRun     *GCPCloudRun     `json:"cloud_run"`
	LoadBalancer *GCPLoadBalancer `json:"load_balancer"`

	// TODO: support for cleanup of not needed stuff
	Other map[string]interface{} `json:"-" mapstructure:",remain"`
}

func (s *StaticApp) Decode(in interface{}) error {
	return mapstructure.Decode(in, s)
}
