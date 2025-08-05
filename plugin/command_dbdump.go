package plugin

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/netutil"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/util/command"
	"github.com/outblocks/outblocks-plugin-go/util/errgroup"
)

func (p *Plugin) runFuncOnDBConnection(ctx context.Context, dep *apiv1.DependencyState, cloudsqluser *gcp.CloudSQLUser, f func(int) error) error {
	port, err := p.databaseDefaultPort(dep, 0)
	if err != nil {
		return err
	}

	port += 10000

	p.log.Infoln("Creating proxy connection...")

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

	cmd, err := p.prepareDBProxyCommand(ctx, dep, cloudsqluser, port, "0.0.0.0", credentialFile, true)
	if err != nil {
		return err
	}

	g, _ := errgroup.WithContext(ctx)
	commandCtx, commandCancel := context.WithCancel(ctx)

	g.Go(func() error { return p.execProxyCommand(commandCtx, cmd, true) })

	ready := netutil.WaitForSocket(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port), 30*time.Second)

	if !ready {
		commandCancel()

		return fmt.Errorf("connection not ready after timeout")
	}

	err = f(port)

	commandCancel()

	_ = g.Wait()

	return err
}

func databaseDockerImage(version string) string {
	if strings.HasPrefix(version, "MYSQL_") {
		return "schnitzler/mysqldump:3.9"
	}

	switch version {
	case "POSTGRES_9_6":
		return "postgres:9-alpine"
	case "POSTGRES_10":
		return "postgres:10-alpine"
	case "POSTGRES_11":
		return "postgres:11-alpine"
	case "POSTGRES_12":
		return "postgres:12-alpine"
	case "POSTGRES_13":
		return "postgres:13-alpine"
	case "POSTGRES_14":
		return "postgres:14-alpine"
	}

	return ""
}

func databaseDockerPostgresDumpArgs(port int, database, user, password string, tables, excludeTables []any, verbose, override bool, additionalArgs []string) (args, env []string) {
	env = []string{
		"PGHOST=host.docker.internal",
		fmt.Sprintf("PGPORT=%d", port),
		fmt.Sprintf("PGDATABASE=%s", database),
		fmt.Sprintf("PGUSER=%s", user),
		fmt.Sprintf("PGPASSWORD=%s", password),
	}

	args = []string{"pg_dump"}

	if !override {
		args = append(args, "--format=custom", "--clean", "--no-owner")
	}

	if verbose {
		args = append(args, "--verbose")
	}

	for _, t := range tables {
		args = append(args, fmt.Sprintf("--table=%s", t))
	}

	for _, t := range excludeTables {
		args = append(args, fmt.Sprintf("--exclude-table=%s", t))
	}

	args = append(args, additionalArgs...)

	return args, env
}

func databaseDockerMySQLDumpArgs(port int, database, user, password string, tables, excludeTables []any, verbose, override bool, additionalArgs []string) (args, env []string) {
	env = []string{
		"MYSQL_HOST=host.docker.internal",
		fmt.Sprintf("MYSQL_TCP_PORT=%d", port),
		fmt.Sprintf("MYSQL_PWD=%s", password),
	}
	args = []string{"mysqldump", fmt.Sprintf("--user=%s", user)}

	if !override {
		args = append(args, "--single-transaction", "--compress", "--routines")
	}

	if verbose {
		args = append(args, "--verbose")
	}

	ignoredTables := make([]string, len(excludeTables))

	for i, t := range excludeTables {
		ignoredTables[i] = fmt.Sprintf("%s.%s", database, t)
	}

	if len(ignoredTables) > 0 {
		args = append(args, fmt.Sprintf("--ignore-table={%s}", strings.Join(ignoredTables, ",")))
	}

	args = append(args, additionalArgs...)
	args = append(args, database)

	for _, t := range tables {
		args = append(args, fmt.Sprintf("%s", t))
	}

	return args, env
}

func hasHelpParam(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}

	return false
}

func databaseDockerDumpArgs(dbVersion string, port int, database, user, password string, tables, excludeTables []any, verbose, override bool, additionalArgs []string) (args, env []string) {
	switch {
	case strings.HasPrefix(dbVersion, "POSTGRES_"):
		args, env = databaseDockerPostgresDumpArgs(port, database, user, password, tables, excludeTables, verbose, override, additionalArgs)
	case strings.HasPrefix(dbVersion, "MYSQL_"):
		args, env = databaseDockerMySQLDumpArgs(port, database, user, password, tables, excludeTables, verbose, override, additionalArgs)
	default:
		panic("unsupported database version")
	}

	return args, env
}

func (p *Plugin) DBDump(ctx context.Context, req *apiv1.CommandRequest) error {
	flags := req.Args.Flags.AsMap()
	name := flags["name"].(string)                   //nolint:errcheck
	user := flags["user"].(string)                   //nolint:errcheck
	file := flags["file"].(string)                   //nolint:errcheck
	database := flags["database"].(string)           //nolint:errcheck
	verbose := flags["verbose"].(bool)               //nolint:errcheck
	override := flags["override"].(bool)             //nolint:errcheck
	tables := flags["tables"].([]any)                //nolint:errcheck
	excludeTables := flags["exclude-tables"].([]any) //nolint:errcheck
	isHelp := hasHelpParam(req.Args.Positional)

	dep, err := filterDepByName(name, req.DependencyStates)
	if err != nil {
		return err
	}

	cloudsqluser, err := p.extractCloudSQLUser(req.PluginState.Registry, dep.Dependency, user)
	if err != nil {
		return err
	}

	if cloudsqluser == nil {
		return fmt.Errorf("user '%s' not found in database", user)
	}

	opts, err := deploy.NewDatabaseDepOptions(dep.Dependency.Properties.AsMap(), dep.Dependency.Type)
	if err != nil {
		return err
	}

	dockerImage := databaseDockerImage(opts.DatabaseVersion)
	if dockerImage == "" {
		return fmt.Errorf("unsupported database version")
	}

	f, err := os.Create(file)
	if err != nil {
		return fmt.Errorf("cannot create output file '%s': %w", file, err)
	}

	defer f.Close()

	return p.runFuncOnDBConnection(ctx, dep, cloudsqluser, func(port int) error {
		p.log.Infoln("Creating database dump...")

		args, env := databaseDockerDumpArgs(opts.DatabaseVersion, port, database, cloudsqluser.Name.Any(), cloudsqluser.Password.Any(), tables, excludeTables, verbose, override, req.Args.Positional)
		runArgs := []string{
			"run",
			"--rm",
			"--add-host=host.docker.internal:host-gateway",
		}

		if isHelp {
			args = []string{args[0], "--help"}
		}

		p.log.Debugf("Running command: %s\n", strings.Join(args, " "))

		for _, e := range env {
			runArgs = append(runArgs, fmt.Sprintf("--env=%s", e))
		}

		runArgs = append(runArgs, dockerImage)

		cmd, err := command.New(
			exec.Command("docker", append(runArgs, args...)...), //nolint:gosec,noctx
		)
		if err != nil {
			return err
		}

		var stdout []byte

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			if isHelp {
				stdout, _ = io.ReadAll(cmd.Stdout())
			} else {
				_, _ = io.Copy(f, cmd.Stdout())
			}

			wg.Done()
		}()

		go func() {
			s := bufio.NewScanner(cmd.Stderr())

			for s.Scan() {
				p.log.Printf("%s\n", plugin_util.StripAnsiControl(s.Text()))
			}

			wg.Done()
		}()

		err = cmd.Run()
		if err != nil {
			return err
		}

		err = cmd.Wait()

		wg.Wait()

		if isHelp {
			p.log.Print(string(stdout))

			return nil
		}

		if err != nil {
			p.log.Errorf("Error running command: %s\n%s\n", strings.Join(args, " "), err)

			return nil
		}

		p.log.Successf("All done. Dump saved to: '%s'.\n", file)

		return nil
	})
}
