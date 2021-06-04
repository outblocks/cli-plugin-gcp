package deploy

import (
	"fmt"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
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

func ErrIs404(err error) bool {
	if err == nil {
		return false
	}

	e, ok := err.(*googleapi.Error)
	if ok && e.Code == 404 {
		return true
	}

	return false
}

func waitForGlobalOperation(cli *compute.Service, project, name string) error {
	for {
		op, err := cli.GlobalOperations.Wait(project, name).Do()
		if err != nil {
			return err
		}

		if op.Status == "DONE" {
			return nil
		}
	}
}
