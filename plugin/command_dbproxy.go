package plugin

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/outblocks/cli-plugin-gcp/actions"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/fileutil"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/util/command"
)

var cloudSQProxyCleanupTimeout = 30 * time.Second

func downloadCloudSQLProxy(ctx context.Context, target string) error {
	err := os.MkdirAll(filepath.Dir(target), 0o755)
	if err != nil {
		return fmt.Errorf("couldn't create cache path %s: %w", filepath.Dir(target), err)
	}

	downloadURL := fmt.Sprintf("https://storage.googleapis.com/cloudsql-proxy/v%s/cloud_sql_proxy.%s.%s", gcp.CloudSQLVersion, runtime.GOOS, runtime.GOARCH)

	return fileutil.DownloadFile(ctx, downloadURL, target)
}

func (p *Plugin) DBProxy(ctx context.Context, req *apiv1.CommandRequest) error {
	flags := req.Args.Flags.AsMap()
	name := flags["name"]

	var dep *apiv1.DependencyState

	if n, ok := name.(string); ok {
		for _, d := range req.DependencyStates {
			if d.Dependency.Name == n {
				dep = d
				break
			}
		}
	}

	if dep == nil {
		return fmt.Errorf("dependency with name '%s' not found or not deployed yet", name)
	}

	port := int(flags["port"].(float64))

	var defaultPort int

	switch dep.Dependency.Type {
	case actions.DepTypeMySQL:
		defaultPort = 3306
	case actions.DepTypePostgresql:
		defaultPort = 5432
	default:
		return fmt.Errorf("dependency '%s' is of unsupported type: %s", name, dep.Dependency.Type)
	}

	if port == 0 {
		port = defaultPort
	}

	binPath := filepath.Join(p.env.PluginProjectCacheDir(), "cloudsqlproxy", fmt.Sprintf("cloud_sql_proxy_%s", gcp.CloudSQLVersion))

	if !plugin_util.FileExists(binPath) {
		p.log.Infoln("Downloading cloud_sql_proxy at v%s...", gcp.CloudSQLVersion)

		err := downloadCloudSQLProxy(ctx, binPath)
		if err != nil {
			return fmt.Errorf("downloading cloud proxy binary error: %w", err)
		}
	}

	err := os.Chmod(binPath, 0o755)
	if err != nil {
		return err
	}

	connectionName := dep.Dns.Properties.AsMap()["connection_name"]

	cmd, err := command.New(fmt.Sprintf("%s -instances %s=tcp:%d", binPath, connectionName, port))
	if err != nil {
		return err
	}

	prefix := "proxy:"

	// Process stdout/stderr.
	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		s := bufio.NewScanner(cmd.Stdout())

		for s.Scan() {
			p.log.Infof("%s %s\n", prefix, plugin_util.StripAnsiControl(s.Text()))
		}

		wg.Done()
	}()

	go func() {
		s := bufio.NewScanner(cmd.Stderr())

		for s.Scan() {
			p.log.Infof("%s %s\n", prefix, plugin_util.StripAnsiControl(s.Text()))
		}

		wg.Done()
	}()

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("error running cloud sql proxy: %w", err)
	}

	select {
	case <-ctx.Done():
		_ = cmd.Stop(cloudSQProxyCleanupTimeout)
	case <-cmd.WaitChannel():
	}

	wg.Wait()

	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("error running cloud sql proxy: %w", err)
	}

	return nil
}
