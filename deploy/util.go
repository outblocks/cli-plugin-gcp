package deploy

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
)

var (
	_ registry.ResourceDiffCalculator = (*CacheInvalidate)(nil)
)

func hashFile(f string) (string, error) {
	file, err := os.Open(f)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buf := make([]byte, 30*1024)
	hash := md5.New()

	for {
		n, err := file.Read(buf)
		if n > 0 {
			_, err := hash.Write(buf[:n])
			if err != nil {
				return "", err
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return "", err
		}
	}

	sum := hash.Sum(nil)

	return hex.EncodeToString(sum), nil
}

func findFiles(root string, patterns []string) (ret map[string]string, err error) {
	ret = make(map[string]string)

	err = plugin_util.WalkWithExclusions(root, patterns, func(path, rel string, info os.FileInfo) error {
		if info.IsDir() {
			return nil
		}

		hash, err := hashFile(path)
		if err != nil {
			return err
		}

		ret[rel] = hash

		return nil
	})

	return ret, err
}

func addCloudSchedulers(r *registry.Registry, app *apiv1.App, projectID, region string, schedulers []*types.AppScheduler) ([]*gcp.CloudSchedulerJob, error) {
	ret := make([]*gcp.CloudSchedulerJob, 0, len(schedulers))

	for i, sch := range schedulers {
		headers := make(map[string]fields.Field, len(sch.Headers))

		for k, v := range sch.Headers {
			headers[k] = fields.String(v)
		}

		base, err := url.Parse(app.Url)
		if err != nil {
			log.Fatal(err)
		}

		u, err := url.Parse(sch.Path)
		if err != nil {
			log.Fatal(err)
		}

		job := &gcp.CloudSchedulerJob{
			Name:        fields.String(fmt.Sprintf("%d: %s", i+1, sch.Cron)),
			ProjectID:   fields.String(projectID),
			Region:      fields.String(region),
			HTTPMethod:  fields.String(sch.Method),
			HTTPURL:     fields.String(base.ResolveReference(u).String()),
			HTTPHeaders: fields.Map(headers),
		}
		ret = append(ret, job)

		_, err = r.RegisterAppResource(app, fmt.Sprintf("cloud_scheduler_job_%d", i+1), job)
		if err != nil {
			return nil, err
		}
	}

	return ret, nil
}
