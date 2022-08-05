package config

import (
	"context"
	"fmt"
	"os"

	logging "cloud.google.com/go/logging/apiv2"
	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/storage"
	dockerclient "github.com/docker/docker/client"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudfunctions/v1"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
	"google.golang.org/api/run/v1"
	"google.golang.org/api/secretmanager/v1"
	"google.golang.org/api/serviceusage/v1"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

const CredentialsEnvVar = "GCLOUD_SERVICE_KEY"

var errCredentialsMissing = fmt.Errorf(`error getting google credentials!
Supported credentials through environment variables: 'GOOGLE_APPLICATION_CREDENTIALS' pointing to a file or 'GCLOUD_SERVICE_KEY' with file contents.
Alternatively install 'gcloud' and authorize with your account: 'gcloud application-default login'`)

func GoogleCredentials(ctx context.Context, scopes ...string) (cred *google.Credentials, err error) {
	if key := os.Getenv(CredentialsEnvVar); key != "" {
		cred, err = google.CredentialsFromJSON(ctx, []byte(key), scopes...)
		if err != nil {
			return nil, errCredentialsMissing
		}

		return cred, nil
	}

	cred, err = google.FindDefaultCredentials(ctx, scopes...)

	if err != nil {
		return nil, errCredentialsMissing
	}

	return cred, nil
}

func NewGCPStorageClient(ctx context.Context, cred *google.Credentials) (*storage.Client, error) {
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

func NewGCPLoggingClient(ctx context.Context, cred *google.Credentials) (*logging.Client, error) {
	return logging.NewClient(ctx, option.WithCredentials(cred))
}

func NewGCPSecretManagerClient(ctx context.Context, cred *google.Credentials) (*secretmanager.Service, error) {
	return secretmanager.NewService(ctx, option.WithCredentials(cred))
}

func NewGCPCloudfunctionsClient(ctx context.Context, cred *google.Credentials) (*cloudfunctions.Service, error) {
	return cloudfunctions.NewService(ctx, option.WithCredentials(cred))
}

func NewGCPMonitoringUptimeCheckClient(ctx context.Context, cred *google.Credentials) (*monitoring.UptimeCheckClient, error) {
	return monitoring.NewUptimeCheckClient(ctx, option.WithCredentials(cred))
}

func NewGCPMonitoringNotificationChannelClient(ctx context.Context, cred *google.Credentials) (*monitoring.NotificationChannelClient, error) {
	return monitoring.NewNotificationChannelClient(ctx, option.WithCredentials(cred))
}

func NewGCPMonitoringAlertPolicyClient(ctx context.Context, cred *google.Credentials) (*monitoring.AlertPolicyClient, error) {
	return monitoring.NewAlertPolicyClient(ctx, option.WithCredentials(cred))
}
