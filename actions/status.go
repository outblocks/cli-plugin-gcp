package actions

import (
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/outblocks-plugin-go/types"
)

func computeAppDeploymentState(app interface{}) *types.DeploymentState {
	var (
		ok, ready bool
		message   string
	)

	switch appDeploy := app.(type) {
	case *deploy.StaticApp:
		ready, ok = appDeploy.CloudRun.Ready.LookupCurrent()
		message = appDeploy.CloudRun.StatusMessage.Current()
	case *deploy.ServiceApp:
		ready, ok = appDeploy.CloudRun.Ready.LookupCurrent()
		message = appDeploy.CloudRun.StatusMessage.Current()
	}

	if !ok {
		return nil
	}

	return &types.DeploymentState{
		Ready:   ready,
		Message: message,
	}
}

func computeDependencyDNSState(dep interface{}) *types.DNSState {
	var dns *types.DNSState

	switch depDeploy := dep.(type) { //nolint:gocritic
	case *deploy.DatabaseDep:
		if !depDeploy.CloudSQL.IsExisting() {
			return nil
		}

		connInfo := depDeploy.CloudSQL.PublicIP.Current()
		if connInfo == "" {
			connInfo = depDeploy.CloudSQL.PrivateIP.Current()
		}

		if connInfo != "" {
			connInfo = fmt.Sprintf("%s (%s)", connInfo, depDeploy.CloudSQL.ConnectionName.Current())
		}

		dns = &types.DNSState{
			IP:             depDeploy.CloudSQL.PublicIP.Current(),
			InternalIP:     depDeploy.CloudSQL.PrivateIP.Current(),
			ConnectionInfo: connInfo,
			Properties: map[string]interface{}{
				"connection_name": depDeploy.CloudSQL.ConnectionName.Current(),
			},
		}
	}

	return dns
}
