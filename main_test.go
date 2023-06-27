package main

import (
	"context"
	"os"
	"path"
	"testing"
	"time"

	"github.com/efficientgo/core/testutil"
	"github.com/go-kit/log"
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	"github.com/observatorium/obsctl/pkg/config"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type testRulesLoader struct{}

func (r *testRulesLoader) getLokiAlertingRules() ([]lokiv1.AlertingRule, error) {
	return nil, nil
}

func (r *testRulesLoader) getLokiRecordingRules() ([]lokiv1.RecordingRule, error) {
	return nil, nil
}

func (r *testRulesLoader) getPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
	return nil, nil
}

func (k *testRulesLoader) getTenantLogsAlertingRuleGroups(alertingRules []lokiv1.AlertingRule) map[string]lokiv1.AlertingRuleSpec {
	return map[string]lokiv1.AlertingRuleSpec{
		"test": {},
	}
}

func (k *testRulesLoader) getTenantLogsRecordingRuleGroups(recordingRules []lokiv1.RecordingRule) map[string]lokiv1.RecordingRuleSpec {
	return map[string]lokiv1.RecordingRuleSpec{
		"test": {},
	}
}

func (r *testRulesLoader) getTenantMetricsRuleGroups(_ []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
	return map[string]monitoringv1.PrometheusRuleSpec{
		"test": {},
	}
}

type testRulesSyncer struct {
	setCurrentTenantCnt int
	logsRulesCnt        int
	metricsRulesCnt     int
}

func (r *testRulesSyncer) initOrReloadObsctlConfig() error {
	return nil
}

func (r *testRulesSyncer) setCurrentTenant(tenant string) error {
	r.setCurrentTenantCnt++
	return nil
}

func (r *testRulesSyncer) obsctlLogsAlertingSet(rules lokiv1.AlertingRuleSpec) error {
	r.logsRulesCnt++
	return nil
}

func (r *testRulesSyncer) obsctlLogsRecordingSet(rules lokiv1.RecordingRuleSpec) error {
	r.logsRulesCnt++
	return nil
}

func (r *testRulesSyncer) obsctlMetricsSet(rules monitoringv1.PrometheusRuleSpec) error {
	r.metricsRulesCnt++
	return nil
}

func TestSyncLoop(t *testing.T) {
	rl := &testRulesLoader{}
	rs := &testRulesSyncer{}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(25*time.Second, func() { cancel() })

	testutil.Ok(t, syncLoop(ctx, log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)), rl, rs, true, 5))

	testutil.Equals(t, 12, rs.setCurrentTenantCnt)
	testutil.Equals(t, 4, rs.metricsRulesCnt)
	testutil.Equals(t, 8, rs.logsRulesCnt)
}

func TestInitOrReloadObsctlConfig(t *testing.T) {
	o := &obsctlRulesSyncer{
		ctx:             context.TODO(),
		logger:          log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)),
		skipClientCheck: true,
		audience:        "test",
		apiURL:          "http://yolo.com/",
		issuerURL:       "http://yolo-auth.com",
	}
	testutil.Ok(t, os.Setenv("TEST_CLIENT_ID", "test"))
	testutil.Ok(t, os.Setenv("TEST_CLIENT_SECRET", "test"))
	testutil.Ok(t, os.Setenv("YOLO_CLIENT_ID", "test"))
	testutil.Ok(t, os.Setenv("YOLO_CLIENT_SECRET", "test"))

	testOIDC := &config.OIDCConfig{
		Audience:     "test",
		ClientID:     "test",
		ClientSecret: "test",
		IssuerURL:    "http://yolo-auth.com",
	}

	for _, tc := range []struct {
		name             string
		tenants          string
		prexistingConfig *config.Config
		wantConfig       *config.Config
	}{
		{
			name:             "empty existing config",
			prexistingConfig: &config.Config{},
			tenants:          "test",
		},
		{
			name: "already existing config with one tenant",
			prexistingConfig: &config.Config{
				APIs: map[string]config.APIConfig{
					"api": {
						URL: "http://yolo.com/",
						Contexts: map[string]config.TenantConfig{
							"test": {
								OIDC:   testOIDC,
								Tenant: "test",
							},
						},
					},
				},
				Current: struct {
					API    string `json:"api"`
					Tenant string `json:"tenant"`
				}{
					API:    "api",
					Tenant: "test",
				},
			},
			tenants: "test",
		},
		{
			name: "already existing config with multiple tenants",
			prexistingConfig: &config.Config{
				APIs: map[string]config.APIConfig{
					"api": {
						URL: "http://yolo.com/",
						Contexts: map[string]config.TenantConfig{
							"test": {
								OIDC:   testOIDC,
								Tenant: "test",
							},
							"yolo": {
								OIDC:   testOIDC,
								Tenant: "yolo",
							},
						},
					},
				},
				Current: struct {
					API    string `json:"api"`
					Tenant string `json:"tenant"`
				}{
					API:    "api",
					Tenant: "test",
				},
			},
			tenants: "test,yolo",
		},
		{
			name: "new config with one tenant",
			wantConfig: &config.Config{
				APIs: map[string]config.APIConfig{
					"api": {
						URL: "http://yolo.com/",
						Contexts: map[string]config.TenantConfig{
							"test": {
								OIDC:   testOIDC,
								Tenant: "test",
							},
						},
					},
				},
				Current: struct {
					API    string `json:"api"`
					Tenant string `json:"tenant"`
				}{
					API:    "api",
					Tenant: "test",
				},
			},
			tenants: "test",
		},
		{
			name: "new config with multiple tenants",
			wantConfig: &config.Config{
				APIs: map[string]config.APIConfig{
					"api": {
						URL: "http://yolo.com/",
						Contexts: map[string]config.TenantConfig{
							"test": {
								OIDC:   testOIDC,
								Tenant: "test",
							},
							"yolo": {
								OIDC:   testOIDC,
								Tenant: "yolo",
							},
						},
					},
				},
				Current: struct {
					API    string `json:"api"`
					Tenant string `json:"tenant"`
				}{
					API:    "api",
					Tenant: "test",
				},
			},
			tenants: "test,yolo",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.Ok(t, os.Setenv("OBSCTL_CONFIG_PATH", path.Join(dir, "obsctl", "config.json")))

			// Handle test cases with pre-exisiting config.
			if tc.prexistingConfig != nil {
				o.managedTenants = tc.tenants
				testutil.Ok(t, tc.prexistingConfig.Save(o.logger))
				testutil.Ok(t, o.initOrReloadObsctlConfig())

				if len(tc.prexistingConfig.APIs["api"].Contexts) != 0 {
					testutil.Equals(t, tc.prexistingConfig.APIs, o.c.APIs)
					testutil.Equals(t, tc.prexistingConfig.Current, o.c.Current)
				} else {
					// Handle case where pre-existing config is empty.
					testutil.Equals(t, map[string]config.APIConfig{
						"api": {
							URL: "http://yolo.com/",
							Contexts: map[string]config.TenantConfig{
								"test": {
									OIDC:   testOIDC,
									Tenant: "test",
								},
							},
						},
					}, o.c.APIs)
				}
			} else {
				o.managedTenants = tc.tenants
				testutil.Ok(t, o.initOrReloadObsctlConfig())
				testutil.Equals(t, tc.wantConfig, o.c)
			}
		})
	}
}

func TestGetTenantMetricsRuleGroups(t *testing.T) {
	k := &kubeRulesLoader{
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
			testutil.Equals(t, tc.want, k.getTenantMetricsRuleGroups(tc.input))
		})
	}
}

func TestGetTenantLokiAlertingRuleGroups(t *testing.T) {
	k := &kubeRulesLoader{
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
			testutil.Equals(t, tc.want, k.getTenantLogsAlertingRuleGroups(tc.input))
		})
	}
}

func TestGetTenantLokiRecordingRuleGroups(t *testing.T) {
	k := &kubeRulesLoader{
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
			testutil.Equals(t, tc.want, k.getTenantLogsRecordingRuleGroups(tc.input))
		})
	}
}
