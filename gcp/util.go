package gcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/cloudresourcemanager/v1"
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

func WaitForGlobalComputeOperation(cli *compute.Service, project, name string) error {
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

func WaitForRegionComputeOperation(cli *compute.Service, project, region, name string) error {
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

func WaitForServiceUsageOperation(cli *serviceusage.Service, op *serviceusage.Operation) error {
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

func WaitForCloudResourceManagerOperation(cli *cloudresourcemanager.Service, op *cloudresourcemanager.Operation) error {
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

func SplitURL(url string) (host, path string) {
	split := strings.SplitN(url, "://", 2)
	if len(split) == 2 {
		url = split[1]
	}

	urlSplit := strings.SplitN(url, "/", 2)

	if len(urlSplit) == 2 {
		path = urlSplit[1]
	}

	if path == "*" || path == "" {
		path = "/*"
	}

	return urlSplit[0], path
}
