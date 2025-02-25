package syncer

import (
	"context"
	"testing"

	"github.com/go-kit/log"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMetricsSet(t *testing.T) {
	s := scheme.Scheme
	err := monitoringv1.AddToScheme(s)
	require.NoError(t, err)

	interval1m := monitoringv1.Duration("1m")

	tests := []struct {
		name        string
		tenant      string
		inputRules  monitoringv1.PrometheusRuleSpec
		wantErr     bool
		verifyRules func(*testing.T, client.Client)
	}{
		{
			name:   "recording rule with simple expression",
			tenant: "team-a",
			inputRules: monitoringv1.PrometheusRuleSpec{
				Groups: []monitoringv1.RuleGroup{
					{
						Name: "test-group",
						Rules: []monitoringv1.Rule{
							{
								Record: "metric:recording",
								Expr:   intstr.FromString("sum(http_requests_total)"),
							},
						},
					},
				},
			},
			verifyRules: func(t *testing.T, c client.Client) {
				var rules monitoringv1.PrometheusRuleList
				err := c.List(context.Background(), &rules)
				require.NoError(t, err)
				require.Len(t, rules.Items, 1)

				rule := rules.Items[0]
				require.Equal(t, "team-a", rule.Labels["tenant"])
				require.Equal(t, "true", rule.Labels["operator.thanos.io/prometheus-rule"])
				require.Equal(t, "metric:recording", rule.Spec.Groups[0].Rules[0].Record)
				require.Equal(t, `sum(http_requests_total{tenant="team-a"})`, rule.Spec.Groups[0].Rules[0].Expr.String())
			},
		},
		{
			name:   "alerting rule with labels",
			tenant: "team-b",
			inputRules: monitoringv1.PrometheusRuleSpec{
				Groups: []monitoringv1.RuleGroup{
					{
						Name:     "alerts",
						Interval: &interval1m,
						Rules: []monitoringv1.Rule{
							{
								Alert: "HighErrorRate",
								Expr:  intstr.FromString("rate(errors_total[5m]) > 0.1"),
								Labels: map[string]string{
									"severity": "warning",
								},
							},
						},
					},
				},
			},
			verifyRules: func(t *testing.T, c client.Client) {
				var rules monitoringv1.PrometheusRuleList
				err := c.List(context.Background(), &rules)
				require.NoError(t, err)
				require.Len(t, rules.Items, 1)

				rule := rules.Items[0]
				require.Equal(t, "team-b", rule.Labels["tenant"])
				require.Equal(t, "HighErrorRate", rule.Spec.Groups[0].Rules[0].Alert)
				require.Equal(t, "warning", rule.Spec.Groups[0].Rules[0].Labels["severity"])
				require.Equal(t, "team-b", rule.Spec.Groups[0].Rules[0].Labels["tenant"])
				require.Equal(t, `rate(errors_total{tenant="team-b"}[5m]) > 0.1`, rule.Spec.Groups[0].Rules[0].Expr.String())
			},
		},
		{
			name:   "multiple rules in group",
			tenant: "team-c",
			inputRules: monitoringv1.PrometheusRuleSpec{
				Groups: []monitoringv1.RuleGroup{
					{
						Name: "mixed",
						Rules: []monitoringv1.Rule{
							{
								Record: "job:http_requests:rate5m",
								Expr:   intstr.FromString("sum by(job) (rate(http_requests_total[5m]))"),
							},
							{
								Alert: "HighLatency",
								Expr:  intstr.FromString("http_request_duration_seconds > 2"),
							},
						},
					},
				},
			},
			verifyRules: func(t *testing.T, c client.Client) {
				var rules monitoringv1.PrometheusRuleList
				err := c.List(context.Background(), &rules)
				require.NoError(t, err)
				require.Len(t, rules.Items, 1)

				rule := rules.Items[0]
				require.Equal(t, "team-c", rule.Labels["tenant"])
				require.Len(t, rule.Spec.Groups[0].Rules, 2)
				require.Equal(t, `sum by(job) (rate(http_requests_total{tenant="team-c"}[5m]))`, rule.Spec.Groups[0].Rules[0].Expr.String())
				require.Equal(t, `http_request_duration_seconds{tenant="team-c"} > 2`, rule.Spec.Groups[0].Rules[1].Expr.String())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8s := fake.NewClientBuilder().WithScheme(s).Build()

			reg := prometheus.NewRegistry()

			syncer := NewObsctlRulesSyncer(
				context.Background(),
				log.NewNopLogger(),
				k8s,
				"test-namespace",
				"http://test-api",
				"test-audience",
				"test-issuer",
				"team-a,team-b,team-c",
				reg,
			)

			err := syncer.MetricsSet(tt.tenant, tt.inputRules)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.verifyRules != nil {
				tt.verifyRules(t, k8s)
			}
		})
	}
}
