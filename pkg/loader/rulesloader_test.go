package loader

import (
	"context"
	"os"
	"testing"

	"github.com/efficientgo/core/testutil"
	"github.com/go-kit/log"
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestGetTenantMetricsRuleGroups(t *testing.T) {
	k := &KubeRulesLoader{
		ctx:    context.TODO(),
		logger: log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)),
		promTenantRules: promauto.With(prometheus.NewRegistry()).NewGaugeVec(prometheus.GaugeOpts{
			Name: "obsctl_reloader_prom_tenant_rulegroups",
			Help: "Number of Prometheus rules loaded per tenant.",
		}, []string{"tenant"}),
	}

	for _, tc := range []struct {
		name    string
		tenants string
		input   []*monitoringv1.PrometheusRule
		want    map[string]monitoringv1.PrometheusRuleSpec
	}{
		{
			name:    "no rules and no tenants",
			tenants: "",
			input:   []*monitoringv1.PrometheusRule{},
			want:    map[string]monitoringv1.PrometheusRuleSpec{},
		},
		{
			name:    "no rules and one tenant",
			tenants: "test",
			input:   []*monitoringv1.PrometheusRule{},
			want:    map[string]monitoringv1.PrometheusRuleSpec{"test": {Groups: []monitoringv1.RuleGroup{}}},
		},
		{
			name:    "one tenant with one rulegroup",
			tenants: "test",
			input: []*monitoringv1.PrometheusRule{
				{
					Spec: monitoringv1.PrometheusRuleSpec{
						Groups: []monitoringv1.RuleGroup{
							{
								Name:     "TestGroup",
								Interval: "30s",
								Rules: []monitoringv1.Rule{
									{
										Record: "TestRecordingRule",
										Expr:   intstr.FromString("vector(1)"),
									},
								},
							},
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"tenant": "test",
						},
					},
				},
			},
			want: map[string]monitoringv1.PrometheusRuleSpec{
				"test": {
					Groups: []monitoringv1.RuleGroup{
						{
							Name:     "TestGroup",
							Interval: "30s",
							Rules: []monitoringv1.Rule{
								{
									Record: "TestRecordingRule",
									Expr:   intstr.FromString("vector(1)"),
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "one tenant with multiple rulegroups",
			tenants: "test",
			input: []*monitoringv1.PrometheusRule{
				{
					Spec: monitoringv1.PrometheusRuleSpec{
						Groups: []monitoringv1.RuleGroup{
							{
								Name:     "TestGroup",
								Interval: "30s",
								Rules: []monitoringv1.Rule{
									{
										Record: "TestRecordingRule",
										Expr:   intstr.FromString("vector(1)"),
									},
								},
							},
							{
								Name:     "TestGroup2",
								Interval: "1m",
								Rules: []monitoringv1.Rule{
									{
										Alert: "TestAlertingRule",
										Expr:  intstr.FromString("vector(1)"),
									},
								},
							},
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"tenant": "test",
						},
					},
				},
			},
			want: map[string]monitoringv1.PrometheusRuleSpec{
				"test": {
					Groups: []monitoringv1.RuleGroup{
						{
							Name:     "TestGroup",
							Interval: "30s",
							Rules: []monitoringv1.Rule{
								{
									Record: "TestRecordingRule",
									Expr:   intstr.FromString("vector(1)"),
								},
							},
						},
						{
							Name:     "TestGroup2",
							Interval: "1m",
							Rules: []monitoringv1.Rule{
								{
									Alert: "TestAlertingRule",
									Expr:  intstr.FromString("vector(1)"),
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "multiple tenants with multiple rulegroups",
			tenants: "test,yolo",
			input: []*monitoringv1.PrometheusRule{
				{
					Spec: monitoringv1.PrometheusRuleSpec{
						Groups: []monitoringv1.RuleGroup{
							{
								Name:     "TestGroup",
								Interval: "30s",
								Rules: []monitoringv1.Rule{
									{
										Record: "TestRecordingRule",
										Expr:   intstr.FromString("vector(1)"),
									},
								},
							},
							{
								Name:     "TestGroup2",
								Interval: "1m",
								Rules: []monitoringv1.Rule{
									{
										Alert: "TestAlertingRule",
										Expr:  intstr.FromString("vector(1)"),
									},
								},
							},
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"tenant": "test",
						},
					},
				},
				{
					Spec: monitoringv1.PrometheusRuleSpec{
						Groups: []monitoringv1.RuleGroup{
							{
								Name:     "TestYoloGroup",
								Interval: "30s",
								Rules: []monitoringv1.Rule{
									{
										Record: "TestYoloRule",
										Expr:   intstr.FromString("vector(1)"),
									},
								},
							},
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"tenant": "yolo",
						},
					},
				},
			},
			want: map[string]monitoringv1.PrometheusRuleSpec{
				"test": {
					Groups: []monitoringv1.RuleGroup{
						{
							Name:     "TestGroup",
							Interval: "30s",
							Rules: []monitoringv1.Rule{
								{
									Record: "TestRecordingRule",
									Expr:   intstr.FromString("vector(1)"),
								},
							},
						},
						{
							Name:     "TestGroup2",
							Interval: "1m",
							Rules: []monitoringv1.Rule{
								{
									Alert: "TestAlertingRule",
									Expr:  intstr.FromString("vector(1)"),
								},
							},
						},
					},
				},
				"yolo": {
					Groups: []monitoringv1.RuleGroup{
						{
							Name:     "TestYoloGroup",
							Interval: "30s",
							Rules: []monitoringv1.Rule{
								{
									Record: "TestYoloRule",
									Expr:   intstr.FromString("vector(1)"),
								},
							},
						},
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k.managedTenants = tc.tenants
			testutil.Equals(t, tc.want, k.GetTenantMetricsRuleGroups(tc.input))
		})
	}
}

func TestGetTenantLokiAlertingRuleGroups(t *testing.T) {
	k := &KubeRulesLoader{
		ctx:    context.TODO(),
		logger: log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)),
		lokiTenantRules: promauto.With(prometheus.NewRegistry()).NewGaugeVec(prometheus.GaugeOpts{
			Name: "obsctl_reloader_loki_tenant_rulegroups",
			Help: "Number of Loki rules loaded per tenant.",
		}, []string{"type", "tenant"}),
	}

	for _, tc := range []struct {
		name    string
		tenants string
		input   []lokiv1.AlertingRule
		want    map[string]lokiv1.AlertingRuleSpec
	}{
		{
			name:    "no rules and no tenants",
			tenants: "",
			input:   []lokiv1.AlertingRule{},
			want:    map[string]lokiv1.AlertingRuleSpec{},
		},
		{
			name:    "no rules and one tenant",
			tenants: "test",
			input:   []lokiv1.AlertingRule{},
			want:    map[string]lokiv1.AlertingRuleSpec{"test": {Groups: []*lokiv1.AlertingRuleGroup{}}},
		},
		{
			name:    "one tenant with one rulegroup",
			tenants: "test",
			input: []lokiv1.AlertingRule{
				{
					Spec: lokiv1.AlertingRuleSpec{
						TenantID: "test",
						Groups: []*lokiv1.AlertingRuleGroup{
							{
								Name:     "TestGroup",
								Interval: "30s",
								Rules: []*lokiv1.AlertingRuleGroupSpec{
									{
										Alert: "TestAlertingRule",
										Expr:  "1 > 0",
									},
								},
							},
						},
					},
				},
			},
			want: map[string]lokiv1.AlertingRuleSpec{
				"test": {
					Groups: []*lokiv1.AlertingRuleGroup{
						{
							Name:     "TestGroup",
							Interval: "30s",
							Rules: []*lokiv1.AlertingRuleGroupSpec{
								{
									Alert: "TestAlertingRule",
									Expr:  "1 > 0",
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "one tenant with multiple rulegroup",
			tenants: "test",
			input: []lokiv1.AlertingRule{
				{
					Spec: lokiv1.AlertingRuleSpec{
						TenantID: "test",
						Groups: []*lokiv1.AlertingRuleGroup{
							{
								Name:     "TestGroup0",
								Interval: "30s",
								Rules: []*lokiv1.AlertingRuleGroupSpec{
									{
										Alert: "TestAlertingRule0",
										Expr:  "1 > 0",
									},
								},
							},
							{
								Name:     "TestGroup1",
								Interval: "30s",
								Rules: []*lokiv1.AlertingRuleGroupSpec{
									{
										Alert: "TestAlertingRule1",
										Expr:  "1 > 0",
									},
								},
							},
						},
					},
				},
			},
			want: map[string]lokiv1.AlertingRuleSpec{
				"test": {
					Groups: []*lokiv1.AlertingRuleGroup{
						{
							Name:     "TestGroup0",
							Interval: "30s",
							Rules: []*lokiv1.AlertingRuleGroupSpec{
								{
									Alert: "TestAlertingRule0",
									Expr:  "1 > 0",
								},
							},
						},
						{
							Name:     "TestGroup1",
							Interval: "30s",
							Rules: []*lokiv1.AlertingRuleGroupSpec{
								{
									Alert: "TestAlertingRule1",
									Expr:  "1 > 0",
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "multiple tenant with multiple rulegroup",
			tenants: "test,yolo",
			input: []lokiv1.AlertingRule{
				{
					Spec: lokiv1.AlertingRuleSpec{
						TenantID: "test",
						Groups: []*lokiv1.AlertingRuleGroup{
							{
								Name:     "TestGroup0",
								Interval: "30s",
								Rules: []*lokiv1.AlertingRuleGroupSpec{
									{
										Alert: "TestAlertingRule0",
										Expr:  "1 > 0",
									},
								},
							},
							{
								Name:     "TestGroup1",
								Interval: "30s",
								Rules: []*lokiv1.AlertingRuleGroupSpec{
									{
										Alert: "TestAlertingRule1",
										Expr:  "1 > 0",
									},
								},
							},
						},
					},
				},
				{
					Spec: lokiv1.AlertingRuleSpec{
						TenantID: "yolo",
						Groups: []*lokiv1.AlertingRuleGroup{
							{
								Name:     "YoloGroup0",
								Interval: "30s",
								Rules: []*lokiv1.AlertingRuleGroupSpec{
									{
										Alert: "YoloAlertingRule0",
										Expr:  "1 > 0",
									},
								},
							},
							{
								Name:     "YoloGroup1",
								Interval: "30s",
								Rules: []*lokiv1.AlertingRuleGroupSpec{
									{
										Alert: "YoloAlertingRule1",
										Expr:  "1 > 0",
									},
								},
							},
						},
					},
				},
			},
			want: map[string]lokiv1.AlertingRuleSpec{
				"test": {
					Groups: []*lokiv1.AlertingRuleGroup{
						{
							Name:     "TestGroup0",
							Interval: "30s",
							Rules: []*lokiv1.AlertingRuleGroupSpec{
								{
									Alert: "TestAlertingRule0",
									Expr:  "1 > 0",
								},
							},
						},
						{
							Name:     "TestGroup1",
							Interval: "30s",
							Rules: []*lokiv1.AlertingRuleGroupSpec{
								{
									Alert: "TestAlertingRule1",
									Expr:  "1 > 0",
								},
							},
						},
					},
				},
				"yolo": {
					Groups: []*lokiv1.AlertingRuleGroup{
						{
							Name:     "YoloGroup0",
							Interval: "30s",
							Rules: []*lokiv1.AlertingRuleGroupSpec{
								{
									Alert: "YoloAlertingRule0",
									Expr:  "1 > 0",
								},
							},
						},
						{
							Name:     "YoloGroup1",
							Interval: "30s",
							Rules: []*lokiv1.AlertingRuleGroupSpec{
								{
									Alert: "YoloAlertingRule1",
									Expr:  "1 > 0",
								},
							},
						},
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k.managedTenants = tc.tenants
			testutil.Equals(t, tc.want, k.GetTenantLogsAlertingRuleGroups(tc.input))
		})
	}
}

func TestGetTenantLokiRecordingRuleGroups(t *testing.T) {
	k := &KubeRulesLoader{
		ctx:    context.TODO(),
		logger: log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)),
		lokiTenantRules: promauto.With(prometheus.NewRegistry()).NewGaugeVec(prometheus.GaugeOpts{
			Name: "obsctl_reloader_loki_tenant_rulegroups",
			Help: "Number of Loki rules loaded per tenant.",
		}, []string{"type", "tenant"}),
	}

	for _, tc := range []struct {
		name    string
		tenants string
		input   []lokiv1.RecordingRule
		want    map[string]lokiv1.RecordingRuleSpec
	}{
		{
			name:    "no rules and no tenants",
			tenants: "",
			input:   []lokiv1.RecordingRule{},
			want:    map[string]lokiv1.RecordingRuleSpec{},
		},
		{
			name:    "no rules and one tenant",
			tenants: "test",
			input:   []lokiv1.RecordingRule{},
			want:    map[string]lokiv1.RecordingRuleSpec{"test": {Groups: []*lokiv1.RecordingRuleGroup{}}},
		},
		{
			name:    "one tenant with one rulegroup",
			tenants: "test",
			input: []lokiv1.RecordingRule{
				{
					Spec: lokiv1.RecordingRuleSpec{
						TenantID: "test",
						Groups: []*lokiv1.RecordingRuleGroup{
							{
								Name:     "TestGroup",
								Interval: "30s",
								Rules: []*lokiv1.RecordingRuleGroupSpec{
									{
										Record: "TestRecordingRule",
										Expr:   "1 > 0",
									},
								},
							},
						},
					},
				},
			},
			want: map[string]lokiv1.RecordingRuleSpec{
				"test": {
					Groups: []*lokiv1.RecordingRuleGroup{
						{
							Name:     "TestGroup",
							Interval: "30s",
							Rules: []*lokiv1.RecordingRuleGroupSpec{
								{
									Record: "TestRecordingRule",
									Expr:   "1 > 0",
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "one tenant with multiple rulegroup",
			tenants: "test",
			input: []lokiv1.RecordingRule{
				{
					Spec: lokiv1.RecordingRuleSpec{
						TenantID: "test",
						Groups: []*lokiv1.RecordingRuleGroup{
							{
								Name:     "TestGroup0",
								Interval: "30s",
								Rules: []*lokiv1.RecordingRuleGroupSpec{
									{
										Record: "TestRecordingRule0",
										Expr:   "1 > 0",
									},
								},
							},
							{
								Name:     "TestGroup1",
								Interval: "30s",
								Rules: []*lokiv1.RecordingRuleGroupSpec{
									{
										Record: "TestRecordingRule1",
										Expr:   "1 > 0",
									},
								},
							},
						},
					},
				},
			},
			want: map[string]lokiv1.RecordingRuleSpec{
				"test": {
					Groups: []*lokiv1.RecordingRuleGroup{
						{
							Name:     "TestGroup0",
							Interval: "30s",
							Rules: []*lokiv1.RecordingRuleGroupSpec{
								{
									Record: "TestRecordingRule0",
									Expr:   "1 > 0",
								},
							},
						},
						{
							Name:     "TestGroup1",
							Interval: "30s",
							Rules: []*lokiv1.RecordingRuleGroupSpec{
								{
									Record: "TestRecordingRule1",
									Expr:   "1 > 0",
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "multiple tenant with multiple rulegroup",
			tenants: "test,yolo",
			input: []lokiv1.RecordingRule{
				{
					Spec: lokiv1.RecordingRuleSpec{
						TenantID: "test",
						Groups: []*lokiv1.RecordingRuleGroup{
							{
								Name:     "TestGroup0",
								Interval: "30s",
								Rules: []*lokiv1.RecordingRuleGroupSpec{
									{
										Record: "TestRecordingRule0",
										Expr:   "1 > 0",
									},
								},
							},
							{
								Name:     "TestGroup1",
								Interval: "30s",
								Rules: []*lokiv1.RecordingRuleGroupSpec{
									{
										Record: "TestRecordingRule1",
										Expr:   "1 > 0",
									},
								},
							},
						},
					},
				},
				{
					Spec: lokiv1.RecordingRuleSpec{
						TenantID: "yolo",
						Groups: []*lokiv1.RecordingRuleGroup{
							{
								Name:     "YoloGroup0",
								Interval: "30s",
								Rules: []*lokiv1.RecordingRuleGroupSpec{
									{
										Record: "YoloRecordingRule0",
										Expr:   "1 > 0",
									},
								},
							},
							{
								Name:     "YoloGroup1",
								Interval: "30s",
								Rules: []*lokiv1.RecordingRuleGroupSpec{
									{
										Record: "YoloRecordingRule1",
										Expr:   "1 > 0",
									},
								},
							},
						},
					},
				},
			},
			want: map[string]lokiv1.RecordingRuleSpec{
				"test": {
					Groups: []*lokiv1.RecordingRuleGroup{
						{
							Name:     "TestGroup0",
							Interval: "30s",
							Rules: []*lokiv1.RecordingRuleGroupSpec{
								{
									Record: "TestRecordingRule0",
									Expr:   "1 > 0",
								},
							},
						},
						{
							Name:     "TestGroup1",
							Interval: "30s",
							Rules: []*lokiv1.RecordingRuleGroupSpec{
								{
									Record: "TestRecordingRule1",
									Expr:   "1 > 0",
								},
							},
						},
					},
				},
				"yolo": {
					Groups: []*lokiv1.RecordingRuleGroup{
						{
							Name:     "YoloGroup0",
							Interval: "30s",
							Rules: []*lokiv1.RecordingRuleGroupSpec{
								{
									Record: "YoloRecordingRule0",
									Expr:   "1 > 0",
								},
							},
						},
						{
							Name:     "YoloGroup1",
							Interval: "30s",
							Rules: []*lokiv1.RecordingRuleGroupSpec{
								{
									Record: "YoloRecordingRule1",
									Expr:   "1 > 0",
								},
							},
						},
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k.managedTenants = tc.tenants
			testutil.Equals(t, tc.want, k.GetTenantLogsRecordingRuleGroups(tc.input))
		})
	}
}
