package client

import (
	"context"
	"fmt"
	"time"

	"github.com/antihax/optional"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	mlp "github.com/caraml-dev/mlp/api/client"
	"github.com/caraml-dev/mlp/api/pkg/instrumentation/metrics"
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

const (
	MLPSecretsNotFound string = "flyte_dsw_mlp_secrets_not_found"
	MLPRequestsTotal   string = "flyte_dsw_mlp_requests_total"
)

var MLPSecretsNotFoundMetrics = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: MLPSecretsNotFound,
	Help: "Number of occurrence where user requested secrets is not found in MLP API",
},
	[]string{"project"},
)

var MLPRequestsTotalMetrics = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: MLPRequestsTotal,
	Help: "Number of call to MLP API",
},
	[]string{"project", "status"},
)

// GetMLPSecretValue takes in project and secret name and return the secret value/data from mlp client
func (m *APIClient) GetMLPSecretValue(project string, secretName string) (string, error) {

	ctx, cancel := context.WithTimeout(context.Background(), mlpQueryTimeoutSeconds*time.Second)
	defer cancel()

	var err error
	defer func(err error) {
		status := metrics.GetStatusString(err == nil)
		MLPRequestsTotalMetrics.WithLabelValues(project, status).Inc()
	}(err)

	mlpProject, err := m.getMLPProject(project)
	if err != nil {
		return "", fmt.Errorf("cannot get project from mlp, %v", err.Error())
	}

	secrets, resp, err := m.SecretApi.V1ProjectsProjectIdSecretsGet(ctx, mlpProject.ID)
	if err != nil {
		return "", err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	for _, mlpSecret := range secrets {
		if mlpSecret.Name == secretName {
			return mlpSecret.Data, nil
		}
	}
	MLPSecretsNotFoundMetrics.WithLabelValues(project).Inc()
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
