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
	SecretNotFound metrics.MetricName = "secret_not_found"
	MLPAPISuccess  metrics.MetricName = "mlp_call_success"
	MLPAPIError    metrics.MetricName = "mlp_call_error"
	WebhookSuccess metrics.MetricName = "webhook_success"
	WebhookError   metrics.MetricName = "webhook_error"
)

func GetCounterMetrics() map[metrics.MetricName]metrics.PrometheusCounterVec {

	var counterMap = map[metrics.MetricName]metrics.PrometheusCounterVec{
		SecretNotFound: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   Subsystem,
			Name:        string(SecretNotFound),
			Help:        "Number of occurrence where user requested secrets is not found in MLP API",
			ConstLabels: nil,
		},
			[]string{},
		),
		MLPAPISuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   Subsystem,
			Name:        string(MLPAPISuccess),
			Help:        "Number of successful call to MLP API",
			ConstLabels: nil,
		},
			[]string{},
		),
		MLPAPIError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   Subsystem,
			Name:        string(MLPAPIError),
			Help:        "Number of failed call to MLP API",
			ConstLabels: nil,
		},
			[]string{},
		),
		WebhookSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   Subsystem,
			Name:        string(WebhookSuccess),
			Help:        "Number of success request processed by Webhook",
			ConstLabels: nil,
		},
			[]string{},
		),
		WebhookError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   Subsystem,
			Name:        string(WebhookSuccess),
			Help:        "Number of failed request processed by Webhook",
			ConstLabels: nil,
		},
			[]string{},
		),
	}

	return counterMap
}

func Inc(name metrics.MetricName) {
	if err := metrics.Glob().Inc(name, map[string]string{}); err != nil {
		log.Warnf("error incrementing metrics counter for %v", name)
	}
}
