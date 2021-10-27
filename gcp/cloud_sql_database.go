package gcp

import (
	"context"
	"fmt"
	"os"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

type CloudSQLDatabase struct {
	registry.ResourceBase

	ProjectID fields.StringInputField `state:"force_new"`
	Instance  fields.StringInputField `state:"force_new"`
	Name      fields.StringInputField `state:"force_new"`
}

func (o *CloudSQLDatabase) UniqueID() string {
	return fields.GenerateID("projects/%s/instances/%s/databases/%s", o.ProjectID, o.Instance, o.Name)
}

func (o *CloudSQLDatabase) GetName() string {
	return o.Name.Any()
}

func (o *CloudSQLDatabase) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Any()
	instance := o.Instance.Any()
	name := o.Name.Any()

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	_, err = cli.Instances.Get(projectID, instance).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	}

	_, err = cli.Databases.Get(projectID, instance, name).Do()
	if ErrIs404(err) {
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

func (o *CloudSQLDatabase) Create(ctx context.Context, meta interface{}) error {
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

	op, err := cli.Databases.Insert(projectID, instance, &sqladmin.Database{
		Name: name,
	}).Do()
	if err != nil {
		return err
	}

	return WaitForSQLOperation(ctx, cli, projectID, op.Name)
}

func (o *CloudSQLDatabase) Update(ctx context.Context, meta interface{}) error {
	return fmt.Errorf("unimplemented")
}

func (o *CloudSQLDatabase) Delete(ctx context.Context, meta interface{}) error {
	fmt.Fprintln(os.Stderr, o.Instance.Current(), o.Instance.Any(), o.ProjectID.Any(), o.ProjectID.Current())
	key := instanceMutexKey(o.ProjectID.Current(), o.Instance.Current())
	o.Lock(key)
	defer o.Unlock(key)

	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Current()

	op, err := cli.Databases.Delete(projectID, o.Instance.Current(), o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForSQLOperation(ctx, cli, projectID, op.Name)
}
