package config

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
	"google.golang.org/api/run/v1"
)

func GoogleCredentials(ctx context.Context, scopes ...string) (*google.Credentials, error) {
	return google.FindDefaultCredentials(ctx, scopes...)
}

func NewStorageClient(ctx context.Context, cred *google.Credentials) (*storage.Client, error) {
	return storage.NewClient(ctx, option.WithCredentials(cred))
}

func NewDockerClient() (*dockerclient.Client, error) {
	return dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
}

func NewGCPRunClient(ctx context.Context, cred *google.Credentials, region string) (*run.APIService, error) {
	return run.NewService(ctx, option.WithCredentials(cred), option.WithEndpoint(fmt.Sprintf("https://%s-run.googleapis.com", region)))
}

func NewGCPComputeClient(ctx context.Context, cred *google.Credentials) (*compute.Service, error) {
	return compute.NewService(ctx, option.WithCredentials(cred))
}
