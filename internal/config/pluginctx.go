package config

import (
	"context"
	"sync"

	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"github.com/outblocks/outblocks-plugin-go/env"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/run/v1"
)

type PluginContext struct {
	context.Context
	env      env.Enver
	gcred    *google.Credentials
	settings *Settings

	storageCli *storage.Client
	dockerCli  *dockerclient.Client
	runCliMap  map[string]*run.APIService
	computeCli *compute.Service

	mu struct {
		runCli sync.Mutex
	}
	once struct {
		storageCli, dockerCli, computeCli sync.Once
	}
}

func NewPluginContext(ctx context.Context, e env.Enver, gcred *google.Credentials, settings *Settings) *PluginContext {
	return &PluginContext{
		Context:   ctx,
		env:       e,
		gcred:     gcred,
		settings:  settings,
		runCliMap: make(map[string]*run.APIService),
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

func (c *PluginContext) StorageClient() (*storage.Client, error) {
	var err error

	c.once.storageCli.Do(func() {
		c.storageCli, err = NewStorageClient(c, c.GoogleCredentials())
	})

	return c.storageCli, err
}

func (c *PluginContext) GCPRunClient(region string) (*run.APIService, error) {
	var err error

	c.mu.runCli.Lock()

	cli, ok := c.runCliMap[region]

	if !ok {
		cli, err = NewGCPRunClient(c, c.GoogleCredentials(), region)
	}

	c.runCliMap[region] = cli
	c.mu.runCli.Unlock()

	return cli, err
}

func (c *PluginContext) GCPComputeClient() (*compute.Service, error) {
	var err error

	c.once.computeCli.Do(func() {
		c.computeCli, err = NewGCPComputeClient(c, c.GoogleCredentials())
	})

	return c.computeCli, err
}

func (c *PluginContext) DockerClient() (*dockerclient.Client, error) {
	var err error

	c.once.dockerCli.Do(func() {
		c.dockerCli, err = NewDockerClient()
	})

	return c.dockerCli, err
}
