package loop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-kit/log"
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type mockRulesLoader struct {
	mock.Mock
}

func (m *mockRulesLoader) GetPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
	args := m.Called()
	rules := args.Get(0).([]monitoringv1.PrometheusRule)
	ptrRules := make([]*monitoringv1.PrometheusRule, len(rules))
	for i := range rules {
		ptrRules[i] = &rules[i]
	}
	return ptrRules, args.Error(1)
}

func (m *mockRulesLoader) GetTenantMetricsRuleGroups(rules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
	args := m.Called(rules)
	return args.Get(0).(map[string]monitoringv1.PrometheusRuleSpec)
}

func (m *mockRulesLoader) GetLokiAlertingRules() ([]lokiv1.AlertingRule, error) {
	args := m.Called()
	return args.Get(0).([]lokiv1.AlertingRule), args.Error(1)
}

func (m *mockRulesLoader) GetLokiRecordingRules() ([]lokiv1.RecordingRule, error) {
	args := m.Called()
	return args.Get(0).([]lokiv1.RecordingRule), args.Error(1)
}

func (m *mockRulesLoader) GetTenantLogsAlertingRuleGroups(rules []lokiv1.AlertingRule) map[string]lokiv1.AlertingRuleSpec {
	args := m.Called(rules)
	return args.Get(0).(map[string]lokiv1.AlertingRuleSpec)
}

func (m *mockRulesLoader) GetTenantLogsRecordingRuleGroups(rules []lokiv1.RecordingRule) map[string]lokiv1.RecordingRuleSpec {
	args := m.Called(rules)
	return args.Get(0).(map[string]lokiv1.RecordingRuleSpec)
}

type mockRulesSyncer struct {
	mock.Mock
}

func (m *mockRulesSyncer) MetricsSet(tenant string, rules monitoringv1.PrometheusRuleSpec) error {
	args := m.Called(tenant, rules)
	return args.Error(0)
}

func (m *mockRulesSyncer) InitOrReloadObsctlConfig() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockRulesSyncer) LogsAlertingSet(rules lokiv1.AlertingRuleSpec) error {
	args := m.Called(rules)
	return args.Error(0)
}

func (m *mockRulesSyncer) LogsRecordingSet(rules lokiv1.RecordingRuleSpec) error {
	args := m.Called(rules)
	return args.Error(0)
}

func (m *mockRulesSyncer) SetCurrentTenant(tenant string) error {
	args := m.Called(tenant)
	return args.Error(0)
}

func TestSyncLoop(t *testing.T) {
	tests := []struct {
		name               string
		prometheusRules    []monitoringv1.PrometheusRule
		tenantRuleGroups   map[string]monitoringv1.PrometheusRuleSpec
		expectedSyncCalls  map[string]monitoringv1.PrometheusRuleSpec
		logRulesEnabled    bool
		shouldError        bool
		contextCancelAfter time.Duration
	}{
		{
			name: "multiple tenants with different rules",
			prometheusRules: []monitoringv1.PrometheusRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rule1",
						Labels: map[string]string{
							"tenant": "team-a",
						},
					},
					Spec: monitoringv1.PrometheusRuleSpec{
						Groups: []monitoringv1.RuleGroup{
							{
								Name: "group1",
								Rules: []monitoringv1.Rule{
									{
										Record: "metric:recording",
										Expr:   intstr.FromString("sum(http_requests_total)"),
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rule2",
						Labels: map[string]string{
							"tenant": "team-b",
						},
					},
					Spec: monitoringv1.PrometheusRuleSpec{
						Groups: []monitoringv1.RuleGroup{
							{
								Name: "group2",
								Rules: []monitoringv1.Rule{
									{
										Alert: "HighErrorRate",
										Expr:  intstr.FromString("rate(errors_total[5m]) > 0.1"),
									},
								},
							},
						},
					},
				},
			},
			tenantRuleGroups: map[string]monitoringv1.PrometheusRuleSpec{
				"team-a": {
					Groups: []monitoringv1.RuleGroup{
						{
							Name: "group1",
							Rules: []monitoringv1.Rule{
								{
									Record: "metric:recording",
									Expr:   intstr.FromString("sum(http_requests_total)"),
								},
							},
						},
					},
				},
				"team-b": {
					Groups: []monitoringv1.RuleGroup{
						{
							Name: "group2",
							Rules: []monitoringv1.Rule{
								{
									Alert: "HighErrorRate",
									Expr:  intstr.FromString("rate(errors_total[5m]) > 0.1"),
								},
							},
						},
					},
				},
			},
			logRulesEnabled:    false,
			contextCancelAfter: 6 * time.Second, // Give enough time for config reload
		},
		{
			name:            "error getting prometheus rules",
			prometheusRules: []monitoringv1.PrometheusRule{},
			shouldError:     true,
			logRulesEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := new(mockRulesLoader)
			syncer := new(mockRulesSyncer)
			ctx, cancel := context.WithCancel(context.Background())
			if tt.contextCancelAfter > 0 {
				go func() {
					time.Sleep(tt.contextCancelAfter)
					cancel()
				}()
			} else {
				defer cancel()
			}

			if tt.shouldError {
				loader.On("GetPrometheusRules").Return(tt.prometheusRules, errors.New("test error"))
			} else {
				syncer.On("InitOrReloadObsctlConfig").Return(nil).Maybe()

				loader.On("GetPrometheusRules").Return(tt.prometheusRules, nil)
				ptrRules := make([]*monitoringv1.PrometheusRule, len(tt.prometheusRules))
				for i := range tt.prometheusRules {
					ptrRules[i] = &tt.prometheusRules[i]
				}
				loader.On("GetTenantMetricsRuleGroups", ptrRules).Return(tt.tenantRuleGroups)

				for tenant, ruleSpec := range tt.tenantRuleGroups {
					syncer.On("MetricsSet", tenant, ruleSpec).Return(nil)
				}
			}

			err := SyncLoop(
				ctx,
				log.NewNopLogger(),
				loader,
				syncer,
				tt.logRulesEnabled,
				1,
				5,
			)

			if tt.shouldError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

				loader.AssertExpectations(t)
				syncer.AssertExpectations(t)
			}
		})
	}
}
