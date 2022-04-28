package plugin

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/outblocks/cli-plugin-gcp/deploy"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"github.com/outblocks/outblocks-plugin-go/util/command"
)

func databaseDockerPostgresRestoreArgs(port int, database, user, password string, tables, excludeTables []interface{}, usePsql, verbose, override bool, additionalArgs []string) (args, env []string) {
	env = []string{
		"PGHOST=host.docker.internal",
		fmt.Sprintf("PGPORT=%d", port),
		fmt.Sprintf("PGUSER=%s", user),
		fmt.Sprintf("PGPASSWORD=%s", password),
	}

	if !usePsql {
		args = []string{"pg_restore", fmt.Sprintf("--dbname=%s", database)}
	} else {
		args = []string{"psql", fmt.Sprintf("--dbname=%s", database)}
	}

	if !override {
		args = append(args, "--single-transaction")
	}

	if verbose {
		args = append(args, "--verbose")
	}

	if !usePsql {
		for _, t := range tables {
			args = append(args, fmt.Sprintf("--table=%s", t))
		}

		for _, t := range excludeTables {
			args = append(args, fmt.Sprintf("--exclude-table=%s", t))
		}
	}

	args = append(args, additionalArgs...)

	return args, env
}

func databaseDockerMySQLRestoreArgs(port int, database, user, password string, verbose, override bool, additionalArgs []string) (args, env []string) {
	env = []string{
		"MYSQL_HOST=host.docker.internal",
		fmt.Sprintf("MYSQL_TCP_PORT=%d", port),
		fmt.Sprintf("MYSQL_PWD=%s", password),
	}

	args = []string{"mysql", fmt.Sprintf("--user=%s", user)}

	if !override {
		args = append(args, "--compress")
	}

	if !verbose {
		args = append(args, "--silent")
	}

	args = append(args, additionalArgs...)
	args = append(args, database)

	return args, env
}

func databaseDockerRestoreArgs(dbVersion string, port int, database, user, password string, tables, excludeTables []interface{}, usePsql, verbose, override bool, additionalArgs []string) (args, env []string) {
	switch {
	case strings.HasPrefix(dbVersion, "POSTGRES_"):
		return databaseDockerPostgresRestoreArgs(port, database, user, password, tables, excludeTables, usePsql, verbose, override, additionalArgs)
	case strings.HasPrefix(dbVersion, "MYSQL_"):
		return databaseDockerMySQLRestoreArgs(port, database, user, password, verbose, override, additionalArgs)
	}

	panic("unsupported database version")
}

func (p *Plugin) DBRestore(ctx context.Context, req *apiv1.CommandRequest) error {
	flags := req.Args.Flags.AsMap()
	name := flags["name"].(string)
	user := flags["user"].(string)
	file := flags["file"].(string)
	database := flags["database"].(string)
	verbose := flags["verbose"].(bool)
	usePsql := flags["pg-psql"].(bool)
	override := flags["override"].(bool)
	tables := flags["tables"].([]interface{})
	excludeTables := flags["exclude-tables"].([]interface{})
	isHelp := hasHelpParam(req.Args.Positional)

	dep, err := filterDepByName(name, req.DependencyStates)
	if err != nil {
		return err
	}

	cloudsqluser, err := p.extractCloudSQLUser(req.PluginState.Registry, dep.Dependency, user)
	if err != nil {
		return err
	}

	opts, err := deploy.NewDatabaseDepOptions(dep.Dependency.Properties.AsMap(), dep.Dependency.Type)
	if err != nil {
		return err
	}

	dockerImage := databaseDockerImage(opts.DatabaseVersion)
	if dockerImage == "" {
		return fmt.Errorf("unsupported database version")
	}

	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("cannot open backup file '%s': %w", file, err)
	}

	defer f.Close()

	return p.runFuncOnDBConnection(ctx, dep, cloudsqluser, func(port int) error {
		p.log.Infoln("Restoring database dump...")

		args, env := databaseDockerRestoreArgs(opts.DatabaseVersion, port, database, cloudsqluser.Name.Any(), cloudsqluser.Password.Any(), tables, excludeTables, usePsql, verbose, override, req.Args.Positional)
		runArgs := []string{
			"run",
			"--interactive",
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
			exec.Command("docker", append(runArgs, args...)...),
		)
		if err != nil {
			return err
		}

		var wg sync.WaitGroup
		wg.Add(2)

		cmd.SetStdin(f)

		go func() {
			s := bufio.NewScanner(cmd.Stdout())

			for s.Scan() {
				p.log.Printf("%s\n", plugin_util.StripAnsiControl(s.Text()))
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

		if err != nil {
			p.log.Errorf("Error running command: %s\n%s\n", strings.Join(args, " "), err)

			return nil
		}

		p.log.Successln("All done.")

		return nil
	})
}
