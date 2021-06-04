package deploy

const (
	ACLPublicRead     = "publicRead"
	ACLProjectPrivate = "projectPrivate"

	GCSProxyImageName   = "nginx-gcs-static-proxy:1.20"
	GCSProxyDockerImage = "docker.io/outblocks/nginx-gcs-static-proxy:1.20"
	defaultConcurrency  = 5
)