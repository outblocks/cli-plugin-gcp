package gcp

import (
	"context"
	"fmt"

	"github.com/outblocks/cli-plugin-gcp/internal/config"
	"github.com/outblocks/outblocks-plugin-go/registry"
	"github.com/outblocks/outblocks-plugin-go/registry/fields"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

type CloudSQL struct {
	registry.ResourceBase

	Name            fields.StringInputField `state:"force_new"`
	ProjectID       fields.StringInputField `state:"force_new"`
	Region          fields.StringInputField `state:"force_new"`
	DatabaseVersion fields.StringInputField `state:"force_new"`

	PublicIP       fields.StringOutputField
	PrivateIP      fields.StringOutputField
	ConnectionName fields.StringOutputField

	Tier             fields.StringInputField `default:"db-f1-micro"`
	AvailabilityZone fields.StringInputField `default:"ZONAL"`

	IPConfiguration struct {
		Ipv4Enabled fields.BoolInputField `default:"true"`
	}

	BackupConfiguration struct {
		Enabled   fields.BoolInputField   `default:"true"`
		StartTime fields.StringInputField `default:"05:00"`
	}

	DatabaseFlags fields.MapInputField
}

func (o *CloudSQL) UniqueID() string {
	return fields.GenerateID("projects/%s/instances/%s", o.ProjectID, o.Name)
}

func (o *CloudSQL) GetName() string {
	return o.Name.Any()
}

func (o *CloudSQL) Read(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	projectID := o.ProjectID.Any()
	name := o.Name.Any()

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	inst, err := cli.Instances.Get(projectID, name).Do()
	if ErrIs404(err) {
		o.MarkAsNew()

		return nil
	}

	if err != nil {
		return fmt.Errorf("error fetching cloud sql status: %w", err)
	}

	o.MarkAsExisting()
	o.ProjectID.SetCurrent(projectID)
	o.Name.SetCurrent(name)
	o.Region.SetCurrent(inst.Region)
	o.DatabaseVersion.SetCurrent(inst.DatabaseVersion)

	o.setOutputFields(inst)

	o.Tier.SetCurrent(inst.Settings.Tier)
	o.AvailabilityZone.SetCurrent(inst.Settings.AvailabilityType)
	o.IPConfiguration.Ipv4Enabled.SetCurrent(inst.Settings.IpConfiguration.Ipv4Enabled)
	o.BackupConfiguration.Enabled.SetCurrent(inst.Settings.BackupConfiguration.Enabled)
	o.BackupConfiguration.StartTime.SetCurrent(inst.Settings.BackupConfiguration.StartTime)

	flags := make(map[string]interface{}, len(inst.Settings.DatabaseFlags))
	for _, v := range inst.Settings.DatabaseFlags {
		flags[v.Name] = v.Value
	}

	o.DatabaseFlags.SetCurrent(flags)

	return nil
}

func (o *CloudSQL) Create(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()

	op, err := cli.Instances.Insert(projectID, o.makeDatabaseInstance()).Do()
	if err != nil {
		return err
	}

	err = WaitForSQLOperation(ctx, cli, projectID, op.Name)
	if err != nil {
		return err
	}

	inst, err := cli.Instances.Get(projectID, name).Do()
	if err != nil {
		return err
	}

	o.setOutputFields(inst)

	return nil
}

func (o *CloudSQL) Update(ctx context.Context, meta interface{}) error {
	key := instanceMutexKey(o.ProjectID.Wanted(), o.Name.Wanted())
	o.Lock(key)
	defer o.Unlock(key)

	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Wanted()
	name := o.Name.Wanted()

	op, err := cli.Instances.Update(projectID, o.Name.Current(), o.makeDatabaseInstance()).Do()
	if err != nil {
		return err
	}

	err = WaitForSQLOperation(ctx, cli, projectID, op.Name)
	if err != nil {
		return err
	}

	inst, err := cli.Instances.Get(projectID, name).Do()
	if err != nil {
		return err
	}

	o.setOutputFields(inst)

	return nil
}

func (o *CloudSQL) Delete(ctx context.Context, meta interface{}) error {
	pctx := meta.(*config.PluginContext)

	cli, err := pctx.GCPSQLAdminClient(ctx)
	if err != nil {
		return err
	}

	projectID := o.ProjectID.Current()

	op, err := cli.Instances.Delete(projectID, o.Name.Current()).Do()
	if err != nil {
		return err
	}

	return WaitForSQLOperation(ctx, cli, projectID, op.Name)
}

func (o *CloudSQL) makeDatabaseInstance() *sqladmin.DatabaseInstance {
	flags := []*sqladmin.DatabaseFlags{}

	for k, v := range o.DatabaseFlags.Wanted() {
		flags = append(flags, &sqladmin.DatabaseFlags{Name: k, Value: v.(string)})
	}

	return &sqladmin.DatabaseInstance{
		Name:            o.Name.Wanted(),
		Region:          o.Region.Wanted(),
		DatabaseVersion: o.DatabaseVersion.Wanted(),
		Settings: &sqladmin.Settings{
			Tier:             o.Tier.Wanted(),
			AvailabilityType: o.AvailabilityZone.Wanted(),
			IpConfiguration: &sqladmin.IpConfiguration{
				Ipv4Enabled: o.IPConfiguration.Ipv4Enabled.Wanted(),
			},
			BackupConfiguration: &sqladmin.BackupConfiguration{
				Enabled:   o.BackupConfiguration.Enabled.Wanted(),
				StartTime: o.BackupConfiguration.StartTime.Wanted(),
				Location:  o.Region.Wanted()[:2],
			},
			DatabaseFlags: flags,
		},
	}
}

func (o *CloudSQL) setOutputFields(inst *sqladmin.DatabaseInstance) {
	for _, ip := range inst.IpAddresses {
		switch ip.Type {
		case "PRIVATE":
			o.PrivateIP.SetCurrent(ip.IpAddress)
		case "PRIMARY":
			o.PublicIP.SetCurrent(ip.IpAddress)
		}
	}

	o.ConnectionName.SetCurrent(inst.ConnectionName)
}

func instanceMutexKey(project, instanceName string) string {
	return fmt.Sprintf("google-sql-database-instance-%s-%s", project, instanceName)
}
