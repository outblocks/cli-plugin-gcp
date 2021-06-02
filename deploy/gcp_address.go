package deploy

import "encoding/json"

type GCPAddress struct {
	ID string `json:"id"`
}

func (o *GCPAddress) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPAddress
		Type string `json:"type"`
	}{
		GCPAddress: *o,
		Type:       "gcp_address",
	})
}
