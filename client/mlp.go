package client

import (
	"context"
	"fmt"
	"time"

	"github.com/antihax/optional"

	"github.com/caraml-dev/dap-secret-webhook/instrumentation"
	mlp "github.com/caraml-dev/mlp/api/client"
)

const (
	mlpQueryTimeoutSeconds = 30
)

type MLPClient interface {
	GetMLPSecretValue(project string, name string) (string, error)
}

type APIClient struct {
	mlp.APIClient
}

// GetMLPSecretValue takes in project and secret name and return the secret value/data from mlp client
func (m *APIClient) GetMLPSecretValue(project string, secretName string) (string, error) {

	ctx, cancel := context.WithTimeout(context.Background(), mlpQueryTimeoutSeconds*time.Second)
	defer cancel()

	mlpProject, err := m.getMLPProject(project)
	if err != nil {
		instrumentation.Inc(instrumentation.MLPAPIError)
		return "", fmt.Errorf("cannot get project from mlp, %v", err.Error())
	}

	secrets, resp, err := m.SecretApi.V1ProjectsProjectIdSecretsGet(ctx, mlpProject.ID)
	if err != nil {
		instrumentation.Inc(instrumentation.MLPAPIError)
		return "", err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	instrumentation.Inc(instrumentation.MLPAPISuccess)
	for _, mlpSecret := range secrets {
		if mlpSecret.Name == secretName {
			return mlpSecret.Data, nil
		}
	}
	instrumentation.Inc(instrumentation.SecretNotFound)
	return "", fmt.Errorf("cannot find secret '%v' from mlp project '%v'", secretName, project)
}

func (m *APIClient) getMLPProject(namespace string) (*mlp.Project, error) {

	var options *mlp.ProjectApiV1ProjectsGetOpts
	if len(namespace) > 0 {
		options = &mlp.ProjectApiV1ProjectsGetOpts{
			Name: optional.NewString(namespace),
		}
	}
	projects, resp, err := m.ProjectApi.V1ProjectsGet(context.Background(), options)
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	for _, project := range projects {
		if project.Name == namespace {
			return &project, nil
		}
	}
	return nil, fmt.Errorf("cannot find project '%v'from mlp client", namespace)
}
