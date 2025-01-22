package syncer

import (
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// RulesSyncer implements logic for syncing rules to Observatorium API.
type RulesSyncer interface {
	InitOrReloadObsctlConfig() error
	SetCurrentTenant(tenant string) error

	LogsAlertingSet(rules lokiv1.AlertingRuleSpec) error
	LogsRecordingSet(rules lokiv1.RecordingRuleSpec) error
	MetricsSet(tenant string, rules monitoringv1.PrometheusRuleSpec) error
}
