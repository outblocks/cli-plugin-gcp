package plugin

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/cli-plugin-gcp/internal/fileutil"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
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
		case deploy.DepTypePostgreSQL:
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

func (p *Plugin) prepareTempFileCredentials() (f *os.File, err error) {
	key := os.Getenv(config.CredentialsEnvVar)
	if key == "" {
		return nil, nil
	}

	f, err = os.CreateTemp("", "auth-")
	if err != nil {
		return nil, err
	}

	if _, err = f.WriteString(key); err != nil {
		return nil, err
	}

	err = f.Close()

	return f, err
}

func (p *Plugin) extractCloudSQLUser(registryData []byte, dep *apiv1.Dependency, user string) (*gcp.CloudSQLUser, error) {
	if user == "" {
		return nil, nil
	}

	reg := registry.NewRegistry(nil)

	gcp.RegisterTypes(reg)

	err := reg.Load(registryData)
	if err == nil {
		u := &gcp.CloudSQLUser{}

		if reg.GetDependencyResource(dep, user, u) {
			return u, err
		}
	}

	return nil, nil
}

func (p *Plugin) execProxyCommand(ctx context.Context, cmd *command.Cmd, silent bool) error {
	prefix := "proxy:"

	// Process stdout/stderr.
	var wg sync.WaitGroup

	if !silent {
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
	}

	err := cmd.Run()
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
		return fmt.Errorf("error stopping cloud sql proxy: %w", err)
	}

	return nil
}

func (p *Plugin) prepareDBProxyCommand(ctx context.Context, dep *apiv1.DependencyState, cloudsqluser *gcp.CloudSQLUser, port int, bindAddr, credentialFile string, silent bool) (*command.Cmd, error) {
	if ip := net.ParseIP(bindAddr); ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("invalid bind-addr specified, must be a valid ipv4 address")
	}

	binPath := filepath.Join(p.env.PluginProjectCacheDir(), "cloudsqlproxy", fmt.Sprintf("cloud_sql_proxy_%s", gcp.CloudSQLVersion))

	if !plugin_util.FileExists(binPath) {
		p.log.Infof("Downloading cloud_sql_proxy at v%s...\n", gcp.CloudSQLVersion)

		err := downloadCloudSQLProxy(ctx, binPath)
		if err != nil {
			return nil, fmt.Errorf("downloading cloud proxy binary error: %w", err)
		}
	}

	connectionName := dep.Dns.Properties.AsMap()["connection_name"]

	if !silent {
		p.log.Infof("Creating proxy to dependency: %s, connectionName: %s on %s:%d.\n", dep.Dependency.Name, connectionName, bindAddr, port)
	}

	if !silent {
		if cloudsqluser != nil {
			p.log.Infof("You can connect to it using user='%s', password='%s', host='%s:%d'.\n",
				cloudsqluser.Name.Any(), cloudsqluser.Password.Any(), bindAddr, port)
		} else {
			p.log.Infof("You can specify --user to use already created user or connect to it using credentials you defined and host='%s:%d'.\n", bindAddr, port)
		}
	}

	args := []string{"-instances", fmt.Sprintf("%s=tcp:%s:%d", connectionName, bindAddr, port)}

	if credentialFile != "" {
		args = append(args, "-credential_file", credentialFile)
	}

	cmd, err := command.New(
		exec.Command(binPath, args...), //nolint:noctx
	)
	if err != nil {
		return cmd, err
	}

	return cmd, nil
}

func (p *Plugin) databaseDefaultPort(dep *apiv1.DependencyState, port int) (int, error) {
	var defaultPort int

	switch dep.Dependency.Type {
	case deploy.DepTypeMySQL:
		defaultPort = 3306
	case deploy.DepTypePostgreSQL:
		defaultPort = 5432
	default:
		return 0, fmt.Errorf("dependency '%s' is of unsupported type: %s", dep.Dependency.Name, dep.Dependency.Type)
	}

	if port == 0 {
		port = defaultPort
	}

	return port, nil
}

func (p *Plugin) DBProxy(ctx context.Context, req *apiv1.CommandRequest) error {
	flags := req.Args.Flags.AsMap()
	name := flags["name"].(string)          //nolint:errcheck
	user := flags["user"].(string)          //nolint:errcheck
	port := int(flags["port"].(float64))    //nolint:errcheck
	bindAddr := flags["bind-addr"].(string) //nolint:errcheck
	silent := flags["silent"].(bool)        //nolint:errcheck

	dep, err := filterDepByName(name, req.DependencyStates)
	if err != nil {
		return err
	}

	cloudsqluser, err := p.extractCloudSQLUser(req.PluginState.Registry, dep.Dependency, user)
	if err != nil {
		return err
	}

	port, err = p.databaseDefaultPort(dep, port)
	if err != nil {
		return err
	}

	// Prepare temporary credential file if using GCLOUD_SERVICE_KEY.
	credentialFile := ""

	cred, err := p.prepareTempFileCredentials()
	if err != nil {
		return err
	}

	if cred != nil {
		credentialFile = cred.Name()

		defer os.Remove(credentialFile)
	}

	cmd, err := p.prepareDBProxyCommand(ctx, dep, cloudsqluser, port, bindAddr, credentialFile, silent)
	if err != nil {
		return err
	}

	return p.execProxyCommand(ctx, cmd, silent)
}
