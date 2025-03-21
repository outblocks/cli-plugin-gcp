package deploy

import (
	"fmt"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	"github.com/outblocks/outblocks-plugin-go/resources"
	"github.com/outblocks/outblocks-plugin-go/types"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
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

	Dep   *apiv1.Dependency
	Opts  *DatabaseDepOptions
	Needs map[*apiv1.App]*types.DatabaseDepNeed
}

type DatabaseDepArgs struct {
	ProjectID string
	Region    string
	Needs     map[*apiv1.App]*types.DatabaseDepNeed
}

func NewDatabaseDep(dep *apiv1.Dependency) (*DatabaseDep, error) {
	opts, err := NewDatabaseDepOptions(dep.Properties.AsMap(), dep.Type)
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
	types.DatabaseDepOptions

	DatabaseVersion string `json:"-"`
}

func NewDatabaseDepOptions(in map[string]interface{}, typ string) (*DatabaseDepOptions, error) {
	o := &DatabaseDepOptions{}

	err := plugin_util.MapstructureJSONDecode(in, o)
	if err != nil {
		return nil, err
	}

	// Manual defaults.
	if o.Tier == "" {
		o.Tier = "db-f1-micro"
	}

	o.DatabaseVersion, err = o.databaseVersion(typ)
	if err != nil {
		return nil, err
	}

	return o, nil
}

func (o *DatabaseDepOptions) databaseVersion(typ string) (string, error) {
	version := strings.ToUpper(o.Version)

	switch strings.ToUpper(typ) {
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
		case "14":
			return "POSTGRES_14", nil
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

	return "", fmt.Errorf("unknown database version '%s' and type '%s' combination", o.Version, typ)
}

func (o *DatabaseDepOptions) AvailabilityZone() string {
	if o.HA {
		return "REGIONAL"
	}

	return "ZONAL"
}

func (o *DatabaseDep) Plan(pctx *config.PluginContext, r *registry.Registry, c *DatabaseDepArgs) error {
	o.Needs = c.Needs

	flags := make(map[string]fields.Field, len(o.Opts.Flags))
	for k, v := range o.Opts.Flags {
		flags[k] = fields.String(v)
	}

	// Add cloud sql.
	o.CloudSQL = &gcp.CloudSQL{
		Name:             gcp.RandomIDField(pctx.Env(), o.Dep.Id),
		ProjectID:        fields.String(c.ProjectID),
		Region:           fields.String(c.Region),
		DatabaseVersion:  fields.String(o.Opts.DatabaseVersion),
		Tier:             fields.String(o.Opts.Tier),
		AvailabilityZone: fields.String(o.Opts.AvailabilityZone()),
		DatabaseFlags:    fields.Map(flags),
	}

	_, err := r.RegisterDependencyResource(o.Dep, "cloud_sql", o.CloudSQL)
	if err != nil {
		return err
	}

	// Add databases and users.
	users := make(map[string]*types.DatabaseDepOptionUser)

	for username, u := range o.Opts.Users {
		if u == nil {
			u = &types.DatabaseDepOptionUser{}
		}

		users[username] = u
	}

	for _, n := range c.Needs {
		if _, ok := users[n.User]; !ok {
			users[n.User] = &types.DatabaseDepOptionUser{
				Password: n.Password,
				Hostname: n.Hostname,
			}
		}
	}

	for _, n := range c.Needs {
		err = o.registerDatabase(r, n.Database)
		if err != nil {
			return err
		}
	}

	for u, p := range users {
		err = o.registerUser(r, u, p.Password, p.Hostname)
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

	_, err := r.RegisterDependencyResource(o.Dep, db, o.CloudSQLDatabases[db])
	if err != nil {
		return err
	}

	return nil
}

func (o *DatabaseDep) registerUser(r *registry.Registry, user, password, hostname string) error {
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

		_, err := r.RegisterDependencyResource(o.Dep, user, randomPassword)
		if err != nil {
			return err
		}

		passwordField = randomPassword.Result.Input()
	}

	host := fields.String(hostname)

	if o.Dep.Type != DepTypeMySQL {
		host = nil
	}

	// Add cloud sql user.
	o.CloudSQLUsers[user] = &gcp.CloudSQLUser{
		ProjectID: o.CloudSQL.ProjectID,
		Instance:  o.CloudSQL.Name,
		Name:      fields.String(user),
		Password:  passwordField,
		Hostname:  host,
	}

	_, err := r.RegisterDependencyResource(o.Dep, user, o.CloudSQLUsers[user])
	if err != nil {
		return err
	}

	return nil
}
