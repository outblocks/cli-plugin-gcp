package plugin

import (
	"context"
	"fmt"

	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
)

func (p *Plugin) Command(ctx context.Context, req *apiv1.CommandRequest) (*apiv1.CommandResponse, error) {
	var err error

	switch req.Command {
	case "dbproxy":
		err = p.DBProxy(ctx, req)
	default:
		return nil, fmt.Errorf("unknown command: %s", req.Command)
	}

	return &apiv1.CommandResponse{}, err
}
