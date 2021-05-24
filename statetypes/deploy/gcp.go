package deploy

import "encoding/json"

type GCPCloudRun struct {
	ID string
}

func (r *GCPCloudRun) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPCloudRun
		Type string `json:"type"`
	}{
		GCPCloudRun: *r,
		Type:        "gcp_cloud_run",
	})
}

type GCPLoadBalancer struct {
	ID string
}

func (lb *GCPLoadBalancer) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPLoadBalancer
		Type string `json:"type"`
	}{
		GCPLoadBalancer: *lb,
		Type:            "gcp_load_balancer",
	})
}
