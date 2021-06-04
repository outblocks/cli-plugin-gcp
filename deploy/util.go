package deploy

import (
	"fmt"

	"github.com/mitchellh/mapstructure"
)

type detect struct {
	Type string `mapstructure:"type"`
}

func DetectAppType(i interface{}) (interface{}, error) {
	var d *detect

	err := mapstructure.Decode(i, d)
	if err != nil {
		return nil, err
	}

	switch d.Type { // nolint: gocritic // - more app types will be supported
	case "static_app":
		o := NewStaticApp()
		err = o.Decode(i)

		return o, err
	}

	return nil, fmt.Errorf("undetected state type")
}
