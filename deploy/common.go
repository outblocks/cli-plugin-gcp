package deploy

import (
	"github.com/mitchellh/mapstructure"
)

type Common struct {
	Bucket        *GCPBucket   `json:"bucket"`
	ProxyImage    *GCPImage    `json:"proxy_image" mapstructure:"proxy_image"`
	ProxyCloudRun *GCPCloudRun `json:"proxy_cloud_run" mapstructure:"proxy_cloud_run"`

	// TODO: support for cleanup of not needed stuff
	Other map[string]interface{} `json:"-" mapstructure:",remain"`
}

func NewCommon() *Common {
	return &Common{
		ProxyImage:    &GCPImage{},
		Bucket:        &GCPBucket{},
		ProxyCloudRun: &GCPCloudRun{},
	}
}

func (c *Common) Decode(in interface{}) error {
	return mapstructure.Decode(in, c)
}
