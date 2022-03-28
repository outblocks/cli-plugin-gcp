package actions

import (
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
)

func computeAppDeploymentState(app interface{}) *apiv1.DeploymentState {
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

	return &apiv1.DeploymentState{
		Ready:   ready,
		Message: message,
	}
}

func computeDependencyDNSState(dep interface{}) *apiv1.DNSState {
	var dns *apiv1.DNSState

	switch depDeploy := dep.(type) {
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

		props := plugin_util.MustNewStruct(map[string]interface{}{
			"connection_name": depDeploy.CloudSQL.ConnectionName.Current(),
		})

		dns = &apiv1.DNSState{
			Ip:             depDeploy.CloudSQL.PublicIP.Current(),
			InternalIp:     depDeploy.CloudSQL.PrivateIP.Current(),
			ConnectionInfo: connInfo,
			Properties:     props,
		}

	case *deploy.StorageDep:
		if !depDeploy.Bucket.IsExisting() {
			return nil
		}

		props := plugin_util.MustNewStruct(map[string]interface{}{
			"name":     depDeploy.Bucket.Name.Current(),
			"location": depDeploy.Bucket.Location.Current(),
		})

		dns = &apiv1.DNSState{
			Properties: props,
		}
	}

	return dns
}
