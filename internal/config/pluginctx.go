package config

import (
	"context"
	"fmt"
	"sync"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"github.com/outblocks/outblocks-plugin-go/env"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudfunctions/v1"
	"google.golang.org/api/cloudscheduler/v1"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/run/v1"
	"google.golang.org/api/serviceusage/v1"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

type funcCacheData struct {
	ret interface{}
	err error
}

type PluginContext struct {
	env      env.Enver
	gcred    *google.Credentials
	settings *Settings

	storageCli                       *storage.Client
	dockerCli                        *dockerclient.Client
	runCliMap                        map[string]*run.APIService
	computeCli                       *compute.Service
	serviceusageCli                  *serviceusage.Service
	sqlAdminCli                      *sqladmin.Service
	cloudfunctionsCli                *cloudfunctions.Service
	monitoringUptimeChecksCli        *monitoring.UptimeCheckClient
	monitoringNotificationChannelCli *monitoring.NotificationChannelClient
	monitoringAlertPolicyCli         *monitoring.AlertPolicyClient
	cloudschedulerCli                *cloudscheduler.Service

	funcCache map[string]*funcCacheData

	mu struct {
		runCli, funcCache sync.Mutex
	}
	once struct {
		storageCli, dockerCli, computeCli, serviceusageCli, sqlAdminCli, cloudfunctionsCli,
		monitoringUptimeChecksCli, monitoringNotificationChannelCli, monitoringAlertPolicyCli,
		cloudschedulerCli sync.Once
	}
}

func NewPluginContext(e env.Enver, gcred *google.Credentials, settings *Settings) *PluginContext {
	return &PluginContext{
		env:       e,
		gcred:     gcred,
		settings:  settings,
		runCliMap: make(map[string]*run.APIService),
		funcCache: make(map[string]*funcCacheData),
	}
}

func (c *PluginContext) Settings() *Settings {
	return c.settings
}

func (c *PluginContext) Env() env.Enver {
	return c.env
}

func (c *PluginContext) GoogleCredentials() *google.Credentials {
	return c.gcred
}

func (c *PluginContext) GCPStorageClient(ctx context.Context) (*storage.Client, error) {
	var err error

	c.once.storageCli.Do(func() {
		c.storageCli, err = NewGCPStorageClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp storage client: %w", err)
	}

	return c.storageCli, err
}

func (c *PluginContext) GCPRunClient(ctx context.Context, region string) (*run.APIService, error) {
	c.mu.runCli.Lock()
	defer c.mu.runCli.Unlock()

	cli, ok := c.runCliMap[region]

	if !ok {
		var err error

		cli, err = NewGCPRunClient(ctx, c.GoogleCredentials(), region)
		if err != nil {
			return nil, fmt.Errorf("error creating gcp run client: %w", err)
		}
	}

	c.runCliMap[region] = cli

	return cli, nil
}

func (c *PluginContext) GCPComputeClient(ctx context.Context) (*compute.Service, error) {
	var err error

	c.once.computeCli.Do(func() {
		c.computeCli, err = NewGCPComputeClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp compute client: %w", err)
	}

	return c.computeCli, err
}

func (c *PluginContext) GCPServiceUsageClient(ctx context.Context) (*serviceusage.Service, error) {
	var err error

	c.once.serviceusageCli.Do(func() {
		c.serviceusageCli, err = NewGCPServiceUsageClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp service usage client: %w", err)
	}

	return c.serviceusageCli, err
}

func (c *PluginContext) GCPSQLAdminClient(ctx context.Context) (*sqladmin.Service, error) {
	var err error

	c.once.sqlAdminCli.Do(func() {
		c.sqlAdminCli, err = NewGCPSQLAdminClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp sqladmin client: %w", err)
	}

	return c.sqlAdminCli, err
}

func (c *PluginContext) GCPCloudFunctionsClient(ctx context.Context) (*cloudfunctions.Service, error) {
	var err error

	c.once.cloudfunctionsCli.Do(func() {
		c.cloudfunctionsCli, err = NewGCPCloudFunctionsClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp cloud functions client: %w", err)
	}

	return c.cloudfunctionsCli, err
}

func (c *PluginContext) GCPMonitoringAlertPolicyClient(ctx context.Context) (*monitoring.AlertPolicyClient, error) {
	var err error

	c.once.monitoringAlertPolicyCli.Do(func() {
		c.monitoringAlertPolicyCli, err = NewGCPMonitoringAlertPolicyClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp monitoring alert policy client: %w", err)
	}

	return c.monitoringAlertPolicyCli, err
}

func (c *PluginContext) GCPMonitoringUptimeCheckClient(ctx context.Context) (*monitoring.UptimeCheckClient, error) {
	var err error

	c.once.monitoringUptimeChecksCli.Do(func() {
		c.monitoringUptimeChecksCli, err = NewGCPMonitoringUptimeCheckClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp monitoring uptime check client: %w", err)
	}

	return c.monitoringUptimeChecksCli, err
}

func (c *PluginContext) GCPMonitoringNotificationChannelClient(ctx context.Context) (*monitoring.NotificationChannelClient, error) {
	var err error

	c.once.monitoringNotificationChannelCli.Do(func() {
		c.monitoringNotificationChannelCli, err = NewGCPMonitoringNotificationChannelClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp monitoring notification channel client: %w", err)
	}

	return c.monitoringNotificationChannelCli, err
}

func (c *PluginContext) GCPCloudSchedulerClient(ctx context.Context) (*cloudscheduler.Service, error) {
	var err error

	c.once.cloudschedulerCli.Do(func() {
		c.cloudschedulerCli, err = NewGCPCloudSchedulerClient(ctx, c.GoogleCredentials())
	})

	if err != nil {
		return nil, fmt.Errorf("error creating gcp cloud scheduler client: %w", err)
	}

	return c.cloudschedulerCli, err
}

func (c *PluginContext) DockerClient() (*dockerclient.Client, error) {
	var err error

	c.once.dockerCli.Do(func() {
		c.dockerCli, err = NewDockerClient()
	})

	if err != nil {
		return nil, fmt.Errorf("error creating docker client: %w", err)
	}

	return c.dockerCli, err
}

func (c *PluginContext) FuncCache(key string, f func() (interface{}, error)) (interface{}, error) {
	c.mu.funcCache.Lock()

	cache, ok := c.funcCache[key]
	if !ok {
		ret, err := f()
		cache = &funcCacheData{ret: ret, err: err}
		c.funcCache[key] = cache
	}

	c.mu.funcCache.Unlock()

	return cache.ret, cache.err
}
