package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

type CloudSQLUser struct {
	registry.ResourceBase

	ProjectID fields.StringInputField `state:"force_new"`
	Instance  fields.StringInputField `state:"force_new"`
	Name      fields.StringInputField `state:"force_new"`
	Password  fields.StringInputField
}

func (o *CloudSQLUser) UniqueID() string {
	return fields.GenerateID("projects/%s/instances/%s/users/%s", o.ProjectID, o.Instance, o.Name)
}

func (o *CloudSQLUser) GetName() string {
	return fields.VerboseString(o.Name)
}

func (o *CloudSQLUser) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Any()
	instance := o.Instance.Any()
	name := o.Name.Any()

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	users, err := pctx.FuncCache(fmt.Sprintf("CloudSQLUsers:list:%s:%s", projectID, instance), func() (interface{}, error) {
		_, err = cli.Instances.Get(projectID, instance).Do()
		if ErrIs404(err) {
			return nil, err
		}

		users, err := cli.Users.List(projectID, instance).Do()
		if err != nil {
			return nil, err
		}

		ret := make(map[string]*sqladmin.User)

		for _, u := range users.Items {
			ret[u.Name] = u
		}

		return ret, nil
	})

	var user *sqladmin.User
	if users != nil {
		user = users.(map[string]*sqladmin.User)[name]
	}

	if user == nil || ErrIs404(err) || ErrIs403(err) {
		o.MarkAsNew()

		return nil
	}

	if err != nil {
		return fmt.Errorf("error fetching cloud sql database status: %w", err)
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Instance.SetCurrent(instance)
	o.Name.SetCurrent(name)

	return nil
}

func (o *CloudSQLUser) Create(ctx context.Context, meta interface{}) error {
	key := instanceMutexKey(o.ProjectID.Wanted(), o.Instance.Wanted())
	o.Lock(key)
	defer o.Unlock(key)

	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	instance := o.Instance.Wanted()
	name := o.Name.Wanted()
	password := o.Password.Wanted()

	op, err := cli.Users.Insert(projectID, instance, &sqladmin.User{
		Name:     name,
		Password: password,
	}).Do()
	if err != nil {
		return err
	}

	return WaitForSQLOperation(ctx, cli, projectID, op.Name)
}

func (o *CloudSQLUser) Update(ctx context.Context, meta interface{}) error {
	key := instanceMutexKey(o.ProjectID.Wanted(), o.Instance.Wanted())
	o.Lock(key)
	defer o.Unlock(key)

	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	instance := o.Instance.Wanted()
	name := o.Name.Wanted()
	password := o.Password.Wanted()

	op, err := cli.Users.Update(projectID, instance, &sqladmin.User{Password: password}).Name(name).Do()
	if err != nil {
		return err
	}

	return WaitForSQLOperation(ctx, cli, projectID, op.Name)
}

func (o *CloudSQLUser) Delete(ctx context.Context, meta interface{}) error {
	key := instanceMutexKey(o.ProjectID.Current(), o.Instance.Current())
	o.Lock(key)
	defer o.Unlock(key)

	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Current()

	op, err := cli.Users.Delete(projectID, o.Instance.Current()).Name(o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForSQLOperation(ctx, cli, projectID, op.Name)
}
