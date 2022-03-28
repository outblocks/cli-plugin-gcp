package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/outblocks/cli-plugin-gcp/gcp"
	"github.com/outblocks/cli-plugin-gcp/internal/config"
	apiv1 "github.com/outblocks/outblocks-plugin-go/gen/api/v1"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func addServiceAccountToEditor(crmCli *cloudresourcemanager.Service, projectID, serviceAccountName string) error {
	policy, err := crmCli.Projects.GetIamPolicy(projectID, &cloudresourcemanager.GetIamPolicyRequest{}).Do()
	if err != nil {
		return fmt.Errorf("error getting project iam policy: %w", err)
	}

	var editorRoleBinding *cloudresourcemanager.Binding

	for _, b := range policy.Bindings {
		if b.Role == "roles/editor" {
			editorRoleBinding = b
			break
		}
	}

	if editorRoleBinding == nil {
		editorRoleBinding = &cloudresourcemanager.Binding{
			Role: "roles/editor",
		}

		policy.Bindings = append(policy.Bindings, editorRoleBinding)
	}

	editorRoleBinding.Members = append(editorRoleBinding.Members, fmt.Sprintf("serviceAccount:%s@%s.iam.gserviceaccount.com", serviceAccountName, projectID))

	for _, b := range policy.Bindings {
		var members []string

		for _, m := range b.Members {
			if strings.HasPrefix(m, "deleted:") {
				continue
			}

			members = append(members, m)
		}

		b.Members = members
	}

	_, err = crmCli.Projects.SetIamPolicy(projectID, &cloudresourcemanager.SetIamPolicyRequest{
		Policy: policy,
	}).Do()
	if err != nil {
		return fmt.Errorf("error setting project iam policy: %w", err)
	}

	return nil
}

func (p *Plugin) CreateServiceAccount(ctx context.Context, req *apiv1.CommandRequest) error {
	flags := req.Args.Flags.AsMap()

	name := flags["name"].(string)

	if name == "" {
		name = "outblocks-ci"
	}

	name = plugin_util.SanitizeName(name, true, true)

	iamCli, err := config.NewGCPIAMClient(ctx, p.gcred)
	if err != nil {
		return fmt.Errorf("error creating iam client: %w", err)
	}

	accountID := fmt.Sprintf("projects/%s/serviceAccounts/%s@%s.iam.gserviceaccount.com", p.Settings.ProjectID, name, p.Settings.ProjectID)

	_, err = iamCli.Projects.ServiceAccounts.Get(accountID).Do()
	if err != nil {
		if !gcp.ErrIs404(err) {
			return fmt.Errorf("error checking if service account exists: %w", err)
		}
	} else {
		res, err := p.hostCli.PromptConfirmation(ctx, &apiv1.PromptConfirmationRequest{
			Message: fmt.Sprintf("Service account with name '%s' already exists in project '%s'! Do you want to recreate it?", name, p.Settings.ProjectID),
		})
		if err != nil {
			if s, ok := status.FromError(err); ok && s.Code() == codes.Aborted {
				return nil
			}

			return err
		}

		if !res.Confirmed {
			return nil
		}

		_, err = iamCli.Projects.ServiceAccounts.Delete(accountID).Do()
		if err != nil {
			return fmt.Errorf("error deleting service account: %w", err)
		}
	}

	sa, err := iamCli.Projects.ServiceAccounts.Create(fmt.Sprintf("projects/%s", p.Settings.ProjectID), &iam.CreateServiceAccountRequest{
		AccountId: name,
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: name,
			Description: "Created by Outblocks.",
		},
	}).Do()
	if err != nil {
		return fmt.Errorf("error creating service account: %w", err)
	}

	key, err := iamCli.Projects.ServiceAccounts.Keys.Create(fmt.Sprintf("projects/%s/serviceAccounts/%s", p.Settings.ProjectID, sa.UniqueId), &iam.CreateServiceAccountKeyRequest{
		KeyAlgorithm:   "KEY_ALG_RSA_2048",
		PrivateKeyType: "TYPE_GOOGLE_CREDENTIALS_FILE",
	}).Do()
	if err != nil {
		return fmt.Errorf("error creating service account key: %w", err)
	}

	data, err := base64.StdEncoding.DecodeString(key.PrivateKeyData)
	if err != nil {
		return fmt.Errorf("could not decode service account key: %w", err)
	}

	m := make(map[string]interface{})

	err = json.Unmarshal(data, &m)
	if err != nil {
		return fmt.Errorf("could not decode service account key: %w", err)
	}

	encoded, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("could not decode service account key: %w", err)
	}

	crmCli, err := config.NewGCPCloudResourceManagerClient(ctx, p.gcred)
	if err != nil {
		return fmt.Errorf("error creating cloud resource manager client: %w", err)
	}

	err = addServiceAccountToEditor(crmCli, p.Settings.ProjectID, name)
	if err != nil {
		return err
	}

	p.log.Successf("Created '%s' service account with Editor role!\n", name)
	p.log.Printf("\nTo use it in CI or locally add following environment variable:\nGCLOUD_SERVICE_KEY='%s'\n", string(encoded))

	return nil
}