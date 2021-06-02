package deploy

import (
	"strings"

	"github.com/mitchellh/mapstructure"
)

type StaticApp struct {
	Bucket        *GCPBucket   `json:"bucket"`
	ProxyImage    *GCPImage    `json:"proxy_image" mapstructure:"proxy_image"`
	ProxyCloudRun *GCPCloudRun `json:"proxy_cloud_run" mapstructure:"proxy_cloud_run"`

	// TODO: support for cleanup of not needed stuff
	Other map[string]interface{} `json:"-" mapstructure:",remain"`
}

func NewStaticApp() *StaticApp {
	return &StaticApp{
		ProxyImage:    &GCPImage{},
		Bucket:        &GCPBucket{},
		ProxyCloudRun: &GCPCloudRun{},
	}
}

func (s *StaticApp) Decode(in interface{}) error {
	return mapstructure.Decode(in, s)
}

type StaticAppOptions struct {
	Routing string `mapstructure:"routing"`
}

func (s *StaticAppOptions) IsReactRouting() bool {
	return s.Routing == "" || strings.EqualFold(s.Routing, "react")
}

func (s *StaticAppOptions) Decode(in interface{}) error {
	return mapstructure.Decode(in, s)
}
