package gcp

var (
	APISRequired = []string{"run.googleapis.com", "containerregistry.googleapis.com", "compute.googleapis.com"}
	ValidRegions = []string{"asia-east1", "asia-east2", "asia-northeast1", "asia-northeast2", "asia-northeast3", "asia-south1", "asia-southeast1", "australia-southeast1", "europe-north1", "europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6", "northamerica-northeast1", "southamerica-east1", "us-central1", "us-east1", "us-east4", "us-west1", "us-west2", "us-west3"}
)

const (
	ACLPublicRead     = "publicRead"
	ACLProjectPrivate = "projectPrivate"

	GCSProxyImageName   = "nginx-gcs-static-proxy:1.21"
	GCSProxyDockerImage = "docker.io/outblocks/nginx-gcs-static-proxy:1.21"

	DefaultConcurrency = 5
)