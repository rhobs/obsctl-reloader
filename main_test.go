package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/efficientgo/core/testutil"
	"github.com/go-kit/log"
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"github.com/rhobs/obsctl-reloader/pkg/loop"
)

type testRulesLoader struct{}

func (r *testRulesLoader) GetLokiAlertingRules() ([]lokiv1.AlertingRule, error) {
	return nil, nil
}

func (r *testRulesLoader) GetLokiRecordingRules() ([]lokiv1.RecordingRule, error) {
	return nil, nil
}

func (r *testRulesLoader) GetPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
	return nil, nil
}

func (k *testRulesLoader) GetTenantLogsAlertingRuleGroups(alertingRules []lokiv1.AlertingRule) map[string]lokiv1.AlertingRuleSpec {
	return map[string]lokiv1.AlertingRuleSpec{
		"test": {},
	}
}

func (k *testRulesLoader) GetTenantLogsRecordingRuleGroups(recordingRules []lokiv1.RecordingRule) map[string]lokiv1.RecordingRuleSpec {
	return map[string]lokiv1.RecordingRuleSpec{
		"test": {},
	}
}

func (r *testRulesLoader) GetTenantMetricsRuleGroups(_ []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
	return map[string]monitoringv1.PrometheusRuleSpec{
		"test": {},
	}
}

type testRulesSyncer struct {
	setCurrentTenantCnt int
	logsRulesCnt        int
	metricsRulesCnt     int
}

func (r *testRulesSyncer) InitOrReloadObsctlConfig() error {
	return nil
}

func (r *testRulesSyncer) SetCurrentTenant(tenant string) error {
	r.setCurrentTenantCnt++
	return nil
}

func (r *testRulesSyncer) LogsAlertingSet(rules lokiv1.AlertingRuleSpec) error {
	r.logsRulesCnt++
	return nil
}

func (r *testRulesSyncer) LogsRecordingSet(rules lokiv1.RecordingRuleSpec) error {
	r.logsRulesCnt++
	return nil
}

func (r *testRulesSyncer) MetricsSet(rules monitoringv1.PrometheusRuleSpec) error {
	r.metricsRulesCnt++
	return nil
}

func TestSyncLoop(t *testing.T) {
	rl := &testRulesLoader{}
	rs := &testRulesSyncer{}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(25*time.Second, func() { cancel() })

	testutil.Ok(t, loop.SyncLoop(ctx, log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)), rl, rs, true, 5, 60))

	testutil.Equals(t, 12, rs.setCurrentTenantCnt)
	testutil.Equals(t, 4, rs.metricsRulesCnt)
	testutil.Equals(t, 8, rs.logsRulesCnt)
}
