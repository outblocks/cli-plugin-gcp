package deploy

import "encoding/json"

type GCPLoadBalancer struct {
	ID string `json:"id"`
}

func (o *GCPLoadBalancer) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		GCPLoadBalancer
		Type string `json:"type"`
	}{
		GCPLoadBalancer: *o,
		Type:            "gcp_load_balancer",
	})
}
