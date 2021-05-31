package deploy

import "encoding/json"

type GCPCloudRun struct {
	ID string `json:"id"`
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
