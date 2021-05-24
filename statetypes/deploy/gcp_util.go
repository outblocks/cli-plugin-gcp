package deploy

import (
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/util"
)

func BucketName(project, gcpProject, app string) string {
	return fmt.Sprintf("%s-%s-%s", util.LimitString(util.SanitizeName(app), 44), util.LimitString(util.SHAString(project), 8), util.LimitString(util.SHAString(gcpProject), 8))
}
