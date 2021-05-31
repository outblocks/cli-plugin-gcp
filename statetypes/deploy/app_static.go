package deploy

import (
	"github.com/mitchellh/mapstructure"
)

type StaticApp struct {
	ProxyImage   *GCPImage        `json:"proxy_image" mapstructure:"proxy_image"`
	Bucket       *GCPBucket       `json:"bucket"`
	CloudRun     *GCPCloudRun     `json:"cloud_run" mapstructure:"cloud_run"`
	LoadBalancer *GCPLoadBalancer `json:"load_balancer" mapstructure:"load_balancer"`

	// TODO: support for cleanup of not needed stuff
	Other map[string]interface{} `json:"-" mapstructure:",remain"`
}

func NewStaticApp() *StaticApp {
	return &StaticApp{
		ProxyImage:   &GCPImage{},
		Bucket:       &GCPBucket{},
		CloudRun:     &GCPCloudRun{},
		LoadBalancer: &GCPLoadBalancer{},
	}
}

func (s *StaticApp) Decode(in interface{}) error {
	return mapstructure.Decode(in, s)
}
