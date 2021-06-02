package deploy

import (
	"fmt"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/internal/util"
)

func ID(project, gcpProject, id string) string {
	return fmt.Sprintf("%s-%s", util.LimitString(util.SanitizeName(id), 44), util.LimitString(util.SHAString(gcpProject+project), 8))
}

func RegionToGCR(region string) string {
	region = strings.SplitN(strings.ToUpper(region), "-", 2)[0]

	switch region {
	case "EUROPE":
		return "eu.gcr.io"
	case "ASIA":
		return "asia.gcr.io"
	default:
		return "gcr.io"
	}
}
