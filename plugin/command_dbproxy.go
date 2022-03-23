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

	"github.com/outblocks/cli-plugin-gcp/deploy"
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

	err = fileutil.DownloadFile(ctx, downloadURL, target)
	if err != nil {
		return err
	}

	return os.Chmod(target, 0o755)
}

func filterDepByName(name string, depStates map[string]*apiv1.DependencyState) (*apiv1.DependencyState, error) {
	var deps []*apiv1.DependencyState

	for _, d := range depStates {
		switch d.Dependency.Type {
		case deploy.DepTypeMySQL:
		case deploy.DepTypePostgresql:
		default:
			continue
		}

		deps = append(deps, d)
	}

	var dep *apiv1.DependencyState

	if name == "" {
		switch len(deps) {
		case 1:
			dep = deps[0]
		case 0:
			return nil, fmt.Errorf("no matching dependencies were found")
		default:
			return nil, fmt.Errorf("more than one matching dependencies were found, you need to specify --name")
		}
	} else {
		for _, d := range deps {
			if d.Dependency.Name == name {
				dep = d
				break
			}
		}
	}

	if dep == nil {
		return nil, fmt.Errorf("dependency with name '%s' not found or not deployed yet", name)
	}

	return dep, nil
}

func (p *Plugin) DBProxy(ctx context.Context, req *apiv1.CommandRequest) error {
	flags := req.Args.Flags.AsMap()
	name := flags["name"].(string)

	port := int(flags["port"].(float64))

	var defaultPort int

	dep, err := filterDepByName(name, req.DependencyStates)
	if err != nil {
		return err
	}

	switch dep.Dependency.Type {
	case deploy.DepTypeMySQL:
		defaultPort = 3306
	case deploy.DepTypePostgresql:
		defaultPort = 5432
	default:
		return fmt.Errorf("dependency '%s' is of unsupported type: %s", name, dep.Dependency.Type)
	}

	if port == 0 {
		port = defaultPort
	}

	binPath := filepath.Join(p.env.PluginProjectCacheDir(), "cloudsqlproxy", fmt.Sprintf("cloud_sql_proxy_%s", gcp.CloudSQLVersion))

	if !plugin_util.FileExists(binPath) {
		p.log.Infof("Downloading cloud_sql_proxy at v%s...\n", gcp.CloudSQLVersion)

		err := downloadCloudSQLProxy(ctx, binPath)
		if err != nil {
			return fmt.Errorf("downloading cloud proxy binary error: %w", err)
		}
	}

	connectionName := dep.Dns.Properties.AsMap()["connection_name"]

	opts, err := deploy.NewDatabaseDepOptions(dep.Dependency.Properties.AsMap(), dep.Dependency.Type)
	if err != nil {
		return err
	}

	p.log.Infof("Creating proxy to dependency: %s, connectionName: %s on local port: %d.\n", dep.Dependency.Name, connectionName, port)

	if opts.EnableCloudSQLProxyUser {
		p.log.Infof("You can connect to it using user='cloudsqlproxy', password='cloudsqlproxy', host='127.0.0.1:%d'.\n", port)
	} else {
		p.log.Infof("You can connect to it using credentials you defined and host='127.0.0.1:%d'.\n", port)
	}

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
			p.log.Printf("%s %s\n", prefix, plugin_util.StripAnsiControl(s.Text()))
		}

		wg.Done()
	}()

	go func() {
		s := bufio.NewScanner(cmd.Stderr())

		for s.Scan() {
			p.log.Printf("%s %s\n", prefix, plugin_util.StripAnsiControl(s.Text()))
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
