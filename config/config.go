package config

import (
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	TLSConfig     TLSConfig     `envconfig:"TLS"`
	MLPConfig     MLPConfig     `envconfig:"MLP"`
	WebhookConfig WebhookConfig `envconfig:"WEBHOOK"`
	//InCluster flag is used to init k8 client using rest.InClusterConfig or not
	InCluster bool `envconfig:"INCLUSTER" default:"true"`
}

// TLSConfig holds the file path of the required certs to create the Webhook Config and Server
type TLSConfig struct {
	ServerCertFile string `split_words:"true" required:"true"`
	ServerKeyFile  string `split_words:"true" required:"true"`
	CaCertFile     string `split_words:"true" required:"true"`
}

// WebhookConfig holds the config for the MutatingWebhookConfiguration to be created
// The default assume dap-secret-webhook name and flyte namespace for the service, webhook server and config
type WebhookConfig struct {
	// Name of the MutatingWebhookConfiguration resource
	Name string `split_words:"true" default:"dap-secret-webhook"`
	// Namespace to be deployed, only one config is required per cluster
	Namespace string `split_words:"true" default:"flyte"`
	// WebhookName is the name of the webhook to call. Needs to be qualified name
	WebhookName string `split_words:"true" default:"dap-secret-webhook.flyte.svc.cluster.local"`
	// ServiceName is the name of the service for the webhook to call when a request fulfill the rules
	ServiceName string `split_words:"true" default:"dap-secret-webhook"`
	// ServiceNamespace is the namespace of the service deployed in cluster
	ServiceNamespace string `split_words:"true" default:"flyte"`
	// ServicePort is the port of the service
	ServicePort int32 `split_words:"true" default:"443"`
	// MutatePath is the endpoint of the service to call for mutate function
	MutatePath string `split_words:"true" default:"/mutate"`
}

type MLPConfig struct {
	APIHost string `split_words:"true" required:"true"`
}

func InitConfigEnv() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
