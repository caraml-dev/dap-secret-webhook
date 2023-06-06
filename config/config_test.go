package config

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestInitConfigEnv(t *testing.T) {
	tests := []struct {
		name        string
		envVars     map[string]string
		want        *Config
		expectedErr error
	}{
		{
			name:        "missing tls",
			envVars:     nil,
			want:        nil,
			expectedErr: fmt.Errorf("required key TLS_SERVER_CERT_FILE missing value"),
		},
		{
			name: "missing mlp",
			envVars: map[string]string{
				"TLS_SERVER_CERT_FILE": "",
				"TLS_SERVER_KEY_FILE":  "",
				"TLS_CA_CERT_FILE":     "",
			},
			want:        nil,
			expectedErr: fmt.Errorf("required key MLP_API_HOST missing value"),
		},
		{
			name: "ok with default",
			envVars: map[string]string{
				"TLS_SERVER_CERT_FILE": "",
				"TLS_SERVER_KEY_FILE":  "",
				"TLS_CA_CERT_FILE":     "",
				"MLP_API_HOST":         "",
			},
			want: &Config{
				TLSConfig: TLSConfig{},
				MLPConfig: MLPConfig{},
				WebhookConfig: WebhookConfig{
					Name:             "dap-secret-webhook",
					Namespace:        "flyte",
					WebhookName:      "dap-secret-webhook.flyte.svc.cluster.local",
					ServiceName:      "dap-secret-webhook",
					ServiceNamespace: "flyte",
					ServicePort:      443,
					MutatePath:       "/mutate",
				},
				InCluster: true,
			},
			expectedErr: nil,
		},
		{
			name: "ok with override",
			envVars: map[string]string{
				"TLS_SERVER_CERT_FILE":      "/etc/server-cert.pem",
				"TLS_SERVER_KEY_FILE":       "/etc/server-key.pem",
				"TLS_CA_CERT_FILE":          "/etc/ca-cert.pem",
				"MLP_API_HOST":              "mlp:8080",
				"WEBHOOK_NAME":              "dap",
				"WEBHOOK_NAMESPACE":         "default",
				"WEBHOOK_WEBHOOK_NAME":      "dap.default.svc.cluster.local",
				"WEBHOOK_SERVICE_NAME":      "dap",
				"WEBHOOK_SERVICE_NAMESPACE": "default",
				"WEBHOOK_SERVICE_PORT":      "8080",
				"WEBHOOK_MUTATE_PATH":       "/m",
				"INCLUSTER":                 "false",
			},
			want: &Config{
				TLSConfig: TLSConfig{
					ServerCertFile: "/etc/server-cert.pem",
					ServerKeyFile:  "/etc/server-key.pem",
					CaCertFile:     "/etc/ca-cert.pem",
				},
				MLPConfig: MLPConfig{
					APIHost: "mlp:8080",
				},
				WebhookConfig: WebhookConfig{
					Name:             "dap",
					Namespace:        "default",
					WebhookName:      "dap.default.svc.cluster.local",
					ServiceName:      "dap",
					ServiceNamespace: "default",
					ServicePort:      8080,
					MutatePath:       "/m",
				},
				InCluster: false,
			},
			expectedErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupNewEnv(tt.envVars)
			got, err := InitConfigEnv()
			if tt.expectedErr != nil {
				assert.Equal(t, tt.expectedErr, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func setupNewEnv(envMaps ...map[string]string) {
	os.Clearenv()

	for _, envMap := range envMaps {
		for key, val := range envMap {
			os.Setenv(key, val)
		}
	}
}
