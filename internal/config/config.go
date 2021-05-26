package config

import (
	"context"

	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

func GoogleCredentials(ctx context.Context, scopes ...string) (*google.Credentials, error) {
	return google.FindDefaultCredentials(ctx, scopes...)
}

func NewStorageCli(ctx context.Context, cred *google.Credentials) (*storage.Client, error) {
	return storage.NewClient(ctx, option.WithCredentials(cred))
}

func NewDockerCli() (*dockerclient.Client, error) {
	return dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
}
