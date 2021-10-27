package deploy

import (
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/resources"
	"github.com/outblocks/outblocks-plugin-go/types"
)

type DatabaseDepUser struct {
	User     string
	Password string
}

type DatabaseDepDatabase struct {
	Database string
}

type DatabaseDep struct {
	CloudSQL          *gcp.CloudSQL
	CloudSQLDatabases map[string]*gcp.CloudSQLDatabase
	CloudSQLUsers     map[string]*gcp.CloudSQLUser

	Dep   *types.Dependency
	Opts  *DatabaseDepOptions
	Needs map[*types.App]*DatabaseDepNeed
}

type DatabaseDepArgs struct {
	ProjectID string
	Region    string
	Needs     map[*types.App]*DatabaseDepNeed
}

type DatabaseDepNeed struct {
	User     string `mapstructure:"user"`
	Database string `mapstructure:"database"`
}

func NewDatabaseDepNeed(in interface{}) (*DatabaseDepNeed, error) {
	o := &DatabaseDepNeed{}
	return o, mapstructure.Decode(in, o)
}

func NewDatabaseDep(dep *types.Dependency) (*DatabaseDep, error) {
	opts, err := NewDatabaseDepOptions(dep.Properties)
	if err != nil {
		return nil, err
	}

	return &DatabaseDep{
		CloudSQLDatabases: make(map[string]*gcp.CloudSQLDatabase),
		CloudSQLUsers:     make(map[string]*gcp.CloudSQLUser),
		Dep:               dep,
		Opts:              opts,
	}, nil
}

type DatabaseDepOptions struct {
	Version string `mapstructure:"version"`
}

func NewDatabaseDepOptions(in interface{}) (*DatabaseDepOptions, error) {
	o := &DatabaseDepOptions{}
	return o, mapstructure.Decode(in, o)
}

func (o *DatabaseDep) databaseVersion() (string, error) {
	version := strings.ToUpper(o.Opts.Version)

	switch strings.ToUpper(o.Dep.Type) {
	case "POSTGRES", "POSTGRESQL":
		switch version {
		case "9", "9.6", "9_6":
			return "POSTGRES_9_6", nil
		case "10":
			return "POSTGRES_10", nil
		case "11":
			return "POSTGRES_11", nil
		case "12":
			return "POSTGRES_12", nil
		case "13", "":
			return "POSTGRES_13", nil
		}
	case "MYSQL":
		switch version {
		case "5.1", "5_1":
			return "MYSQL_5_1", nil
		case "5.5", "5_5":
			return "MYSQL_5_5", nil
		case "5.6", "5_6":
			return "MYSQL_5_6", nil
		case "5.7", "5_7", "":
			return "MYSQL_5_7", nil
		case "8.0", "8":
			return "MYSQL_8_0", nil
		}
	}

	return "", fmt.Errorf("unknown database version '%s' and type '%s' combination", o.Opts.Version, o.Dep.Type)
}

func (o *DatabaseDep) Plan(pctx *config.PluginContext, r *registry.Registry, c *DatabaseDepArgs) error {
	databaseVersion, err := o.databaseVersion()
	if err != nil {
		return err
	}

	o.Needs = c.Needs

	// Add cloud sql.
	o.CloudSQL = &gcp.CloudSQL{
		Name:            fields.RandomStringWithPrefix(gcp.ID(pctx.Env().ProjectID(), o.Dep.ID), true, false, true, false, 4),
		ProjectID:       fields.String(c.ProjectID),
		Region:          fields.String(c.Region),
		DatabaseVersion: fields.String(databaseVersion),
	}

	err = r.RegisterDependencyResource(o.Dep, "cloud_sql", o.CloudSQL)
	if err != nil {
		return err
	}

	// Add databases and users.
	for _, n := range c.Needs {
		err = o.registerDatabase(r, n.Database)
		if err != nil {
			return err
		}

		err = o.registerUser(r, n.User, "")
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *DatabaseDep) registerDatabase(r *registry.Registry, db string) error {
	if _, ok := o.CloudSQLDatabases[db]; ok {
		return nil
	}

	// Add cloud sql database.
	o.CloudSQLDatabases[db] = &gcp.CloudSQLDatabase{
		ProjectID: o.CloudSQL.ProjectID,
		Instance:  o.CloudSQL.Name,
		Name:      fields.String(db),
	}

	err := r.RegisterDependencyResource(o.Dep, db, o.CloudSQLDatabases[db])
	if err != nil {
		return err
	}

	return nil
}

func (o *DatabaseDep) registerUser(r *registry.Registry, user, password string) error {
	if _, ok := o.CloudSQLUsers[user]; ok {
		return nil
	}

	var passwordField fields.StringInputField
	if password != "" {
		passwordField = fields.String(password)
	} else {
		randomPassword := &resources.RandomString{
			Name: fields.Sprintf("%s password", user),
		}
		err := r.RegisterDependencyResource(o.Dep, user, randomPassword)
		if err != nil {
			return err
		}

		passwordField = randomPassword.Result.Input()
	}

	// Add cloud sql user.
	o.CloudSQLUsers[user] = &gcp.CloudSQLUser{
		ProjectID: o.CloudSQL.ProjectID,
		Instance:  o.CloudSQL.Name,
		Name:      fields.String(user),
		Password:  passwordField,
	}

	err := r.RegisterDependencyResource(o.Dep, user, o.CloudSQLUsers[user])
	if err != nil {
		return err
	}

	return nil
}
