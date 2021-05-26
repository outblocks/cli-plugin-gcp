package deploy

import (
	"fmt"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/internal/util"
)

func BucketName(project, gcpProject, app string) string {
	return fmt.Sprintf("%s-%s-%s", util.LimitString(util.SanitizeName(app), 44), util.LimitString(util.SHAString(project), 8), util.LimitString(util.SHAString(gcpProject), 8))
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
