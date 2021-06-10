package gcp

import (
	"fmt"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

func ID(project, gcpProject, id string) string {
	sanitizedID := util.SanitizeName(id)

	if len(sanitizedID) > 44 {
		idSHA := util.LimitString(util.SHAString(id), 4)
		sanitizedID = fmt.Sprintf("%s-%s", util.LimitString(sanitizedID, 44), idSHA)
	}

	return fmt.Sprintf("%s-%s", sanitizedID, util.LimitString(util.SHAString(gcpProject+project), 4))
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

func waitForRegionOperation(cli *compute.Service, project, region, name string) error {
	for {
		op, err := cli.RegionOperations.Wait(project, region, name).Do()
		if err != nil {
			return err
		}

		if op.Status == "DONE" {
			return nil
		}
	}
}
