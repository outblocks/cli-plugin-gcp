package gcp

var (
	APISRequired = []string{"run.googleapis.com", "containerregistry.googleapis.com", "compute.googleapis.com"}
)

const (
	ACLPublicRead     = "publicRead"
	ACLProjectPrivate = "projectPrivate"

	GCSProxyImageName   = "nginx-gcs-static-proxy:1.20"
	GCSProxyDockerImage = "docker.io/outblocks/nginx-gcs-static-proxy:1.20"

	DefaultConcurrency = 5
)
