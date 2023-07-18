package loader

import (
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// RulesLoader represents logic for loading and filtering Prometheus or Loki Rule objects
// from a given resource and filtering them by tenants.
type RulesLoader interface {
	GetLokiAlertingRules() ([]lokiv1.AlertingRule, error)
	GetLokiRecordingRules() ([]lokiv1.RecordingRule, error)
	GetTenantLogsAlertingRuleGroups(alertingRules []lokiv1.AlertingRule) map[string]lokiv1.AlertingRuleSpec
	GetTenantLogsRecordingRuleGroups(recordingRules []lokiv1.RecordingRule) map[string]lokiv1.RecordingRuleSpec

	GetPrometheusRules() ([]*monitoringv1.PrometheusRule, error)
	GetTenantMetricsRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec
}
