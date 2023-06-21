package instrumentation

import (
	"github.com/caraml-dev/mlp/api/log"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/caraml-dev/mlp/api/pkg/instrumentation/metrics"
)

const (
	// Namespace is the Prometheus Namespace in all metrics published by the Webhook app
	Namespace string = "flyte"
	// Subsystem is the Prometheus Subsystem in all metrics published by the Webhook app
	Subsystem string = "dsw"
)

const (
	MLPSecretsNotFound   metrics.MetricName = "mlp_secrets_not_found"
	MLPRequestsTotal     metrics.MetricName = "mlp_requests_total"
	WebhookRequestsTotal metrics.MetricName = "webhook_requests_total"
)

func GetCounterMetrics() map[metrics.MetricName]metrics.PrometheusCounterVec {

	var counterMap = map[metrics.MetricName]metrics.PrometheusCounterVec{
		MLPSecretsNotFound: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   Subsystem,
			Name:        string(MLPSecretsNotFound),
			Help:        "Number of occurrence where user requested secrets is not found in MLP API",
			ConstLabels: nil,
		},
			[]string{"project"},
		),
		MLPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   Subsystem,
			Name:        string(MLPRequestsTotal),
			Help:        "Number of call to MLP API",
			ConstLabels: nil,
		},
			[]string{"project", "status"},
		),
		WebhookRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   Subsystem,
			Name:        string(WebhookRequestsTotal),
			Help:        "Number of request processed by Webhook",
			ConstLabels: nil,
		},
			[]string{"project", "status", "operation"},
		),
	}

	return counterMap
}

func Inc(name metrics.MetricName, labels map[string]string) {
	if err := metrics.Glob().Inc(name, labels); err != nil {
		log.Warnf("error incrementing metrics counter for %v", name)
	}
}
