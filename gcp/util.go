package gcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/outblocks/outblocks-plugin-go/env"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/resources"
	"github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/serviceusage/v1"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

var Types = []registry.Resource{
	(*resources.RandomString)(nil),
	(*Address)(nil),
	(*APIService)(nil),
	(*BackendService)(nil),
	(*BucketObject)(nil),
	(*Bucket)(nil),
	(*CloudFunction)(nil),
	(*CloudRun)(nil),
	(*CloudSQLDatabase)(nil),
	(*CloudSQLUser)(nil),
	(*CloudSQL)(nil),
	(*ForwardingRule)(nil),
	(*Image)(nil),
	(*ManagedSSL)(nil),
	(*SelfManagedSSL)(nil),
	(*ServerlessNEG)(nil),
	(*TargetHTTPProxy)(nil),
	(*TargetHTTPSProxy)(nil),
	(*URLMap)(nil),
}

func RegisterTypes(reg *registry.Registry) {
	for _, t := range Types {
		reg.RegisterType(t)
	}
}

func GenericID(id string, suffixes ...string) string {
	sanitizedID := util.SanitizeName(id, false, false)

	return fmt.Sprintf("%s-%s", sanitizedID, ShortShaID(strings.Join(suffixes, "-")))
}

func IDField(e env.Enver, resourceID string) fields.StringInputField {
	return fields.LazyString(func() string { return ID(e, resourceID) })
}

func RandomIDField(e env.Enver, resourceID string) fields.StringInputField {
	return fields.RandomStringWithPrefix(ID(e, resourceID), true, false, true, false, 4)
}

func ID(e env.Enver, resourceID string) string {
	sanitizedID := util.SanitizeName(resourceID, false, false)
	sanitizedEnv := util.LimitString(util.SanitizeName(e.Env(), false, false), 4)

	if len(sanitizedID) > 44 {
		sanitizedID = util.LimitString(sanitizedID, 40) + ShortShaID(sanitizedID)
	}

	return fmt.Sprintf("%s-%s-%s", sanitizedID, sanitizedEnv, ShortShaID(e.ProjectID()))
}

func ImageID(e env.Enver, imageID string) string {
	sanitizedID := util.SanitizeName(imageID, false, false)
	sanitizedEnv := util.LimitString(util.SanitizeName(e.Env(), false, false), 4)

	if len(sanitizedID) > 44 {
		sanitizedID = util.LimitString(sanitizedID, 40) + ShortShaID(sanitizedID)
	}

	return fmt.Sprintf("%s-%s/%s", sanitizedEnv, ShortShaID(e.ProjectID()), sanitizedID)
}

func ShortShaID(id string) string {
	return util.LimitString(util.SHAString(id), 4)
}

func GlobalIDField(e env.Enver, gcpProject, resourceID string) fields.StringInputField {
	return fields.LazyString(func() string { return GlobalID(e, gcpProject, resourceID) })
}

func GlobalRandomIDField(e env.Enver, gcpProject, resourceID string) fields.StringInputField {
	return fields.RandomStringWithPrefix(GlobalID(e, gcpProject, resourceID), true, false, true, false, 4)
}

func GlobalID(e env.Enver, gcpProject, id string) string {
	id = ID(e, id)
	return id + ShortShaID(gcpProject)
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

func checkErrCode(err error, code int) bool {
	if err == nil {
		return false
	}

	if et, ok := err.(*transport.Error); ok && et.StatusCode == code {
		return true
	}

	if e, ok := err.(*googleapi.Error); ok && e.Code == code {
		return true
	}

	return false
}

func ErrIs400(err error) bool {
	return checkErrCode(err, 400)
}

func ErrIs404(err error) bool {
	return checkErrCode(err, 404)
}

func ErrIs403(err error) bool {
	return checkErrCode(err, 403)
}

func ErrExtractMissingAPI(err error) string {
	e := &googleapi.Error{}

	if !errors.As(err, &e) {
		return ""
	}

	for _, d := range e.Details {
		m, ok := d.(map[string]interface{})

		if !ok {
			continue
		}

		if m["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" {
			continue
		}

		meta, ok := m["metadata"]
		if !ok {
			continue
		}

		metamap, ok := meta.(map[string]interface{})
		if !ok {
			continue
		}

		return metamap["service"].(string)
	}

	return ""
}

func ComputeOperationError(err *compute.OperationError) error {
	if err == nil {
		return nil
	}

	var buf bytes.Buffer
	for _, err := range err.Errors {
		buf.WriteString(err.Message + "\n")
	}

	return errors.New(buf.String())
}

func SQLOperationError(err *sqladmin.OperationErrors) error {
	if err == nil {
		return nil
	}

	var buf bytes.Buffer
	for _, err := range err.Errors {
		buf.WriteString(err.Message + "\n")
	}

	return errors.New(buf.String())
}

func WaitForGlobalComputeOperation(cli *compute.Service, project, name string) error {
	for {
		op, err := cli.GlobalOperations.Wait(project, name).Do()
		if err != nil {
			return err
		}

		if op.Status == OperationDone {
			return ComputeOperationError(op.Error)
		}
	}
}

func WaitForRegionComputeOperation(cli *compute.Service, project, region, name string) error {
	for {
		op, err := cli.RegionOperations.Wait(project, region, name).Do()
		if err != nil {
			return err
		}

		if op.Status == OperationDone {
			return ComputeOperationError(op.Error)
		}
	}
}

func WaitForServiceUsageOperation(ctx context.Context, cli *serviceusage.Service, op *serviceusage.Operation) error {
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

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}

		if op.Done {
			if op.Error != nil {
				return errors.New(op.Error.Message)
			}

			return nil
		}
	}
}

func WaitForCloudResourceManagerOperation(ctx context.Context, cli *cloudresourcemanager.Service, op *cloudresourcemanager.Operation) error {
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

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}

		if op.Done {
			if op.Error != nil {
				return errors.New(op.Error.Message)
			}

			return nil
		}
	}
}

func WaitForSQLOperation(ctx context.Context, cli *sqladmin.Service, project, name string) error {
	t := time.NewTicker(time.Second * 5)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			op, err := cli.Operations.Get(project, name).Do()
			if err != nil {
				return err
			}

			if op.Status == OperationDone {
				return SQLOperationError(op.Error)
			}
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
		path = "/" + urlSplit[1]
	}

	if path == "" || path == "/" {
		return urlSplit[0], "/*"
	}

	if strings.HasSuffix(path, "/") {
		path += "*"
	}

	return urlSplit[0], path
}
