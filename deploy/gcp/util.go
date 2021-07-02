package gcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/outblocks/cli-plugin-gcp/internal/util"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/serviceusage/v1"
)

var Types = []registry.Resource{
	(*Address)(nil),
	(*APIService)(nil),
	(*BackendService)(nil),
	(*BucketObject)(nil),
	(*Bucket)(nil),
	(*CloudRun)(nil),
	(*ForwardingRule)(nil),
	(*Image)(nil),
	(*ManagedSSL)(nil),
	(*ServerlessNEG)(nil),
	(*TargetHTTPProxy)(nil),
	(*TargetHTTPSProxy)(nil),
	(*URLMap)(nil),
}

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

	if et, ok := err.(*transport.Error); ok && et.StatusCode == 404 {
		return true
	}

	if e, ok := err.(*googleapi.Error); ok && e.Code == 404 {
		return true
	}

	return false
}

func waitForGlobalComputeOperation(cli *compute.Service, project, name string) error {
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

func waitForRegionComputeOperation(cli *compute.Service, project, region, name string) error {
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

func waitForServiceUsageOperation(cli *serviceusage.Service, op *serviceusage.Operation) error {
	if op.Done {
		return nil
	}

	t := time.NewTicker(time.Second)
	defer t.Stop()

	var err error

	for {
		op, err = cli.Operations.Get(op.Name).Do()
		if err != nil {
			return err
		}

		<-t.C

		if op.Done {
			return nil
		}
	}
}
