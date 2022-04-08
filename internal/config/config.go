package config

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
	"google.golang.org/api/run/v1"
	"google.golang.org/api/serviceusage/v1"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

const CredentialsEnvVar = "GCLOUD_SERVICE_KEY"

func GoogleCredentials(ctx context.Context, scopes ...string) (*google.Credentials, error) {
	if key := os.Getenv(CredentialsEnvVar); key != "" {
		return google.CredentialsFromJSON(ctx, []byte(key), scopes...)
	}

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

func NewGCPServiceUsageClient(ctx context.Context, cred *google.Credentials) (*serviceusage.Service, error) {
	return serviceusage.NewService(ctx, option.WithTokenSource(cred.TokenSource))
}

func NewGCPCloudResourceManagerClient(ctx context.Context, cred *google.Credentials) (*cloudresourcemanager.Service, error) {
	return cloudresourcemanager.NewService(ctx, option.WithCredentials(cred))
}

func NewGCPSQLAdminClient(ctx context.Context, cred *google.Credentials) (*sqladmin.Service, error) {
	return sqladmin.NewService(ctx, option.WithCredentials(cred))
}

func NewGCPIAMClient(ctx context.Context, cred *google.Credentials) (*iam.Service, error) {
	return iam.NewService(ctx, option.WithCredentials(cred))
}
