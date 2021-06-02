package deploy

import "encoding/json"

type GCPManagedSSL struct {
	ID string `json:"id"`
}

func (o *GCPManagedSSL) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPManagedSSL
		Type string `json:"type"`
	}{
		GCPManagedSSL: *o,
		Type:          "gcp_managed_ssl",
	})
}
