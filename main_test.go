package main

import (
	"context"
	"os"
	"path"
	"testing"
	"time"

	"github.com/efficientgo/tools/core/pkg/testutil"
	"github.com/go-kit/log"
	"github.com/observatorium/obsctl/pkg/config"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type testRulesLoader struct{}

func (r *testRulesLoader) GetPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
	return nil, nil
}

func (r *testRulesLoader) GetTenantRuleGroups(_ []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
	return map[string]monitoringv1.PrometheusRuleSpec{
		"test": {},
	}
}

type testRulesSyncer struct {
	counter int
}

func (r *testRulesSyncer) InitOrReloadObsctlConfig() error {
	return nil
}

func (r *testRulesSyncer) SetCurrentTenant(tenant string) error {
	return nil
}

func (r *testRulesSyncer) ObsctlMetricsSet(rules monitoringv1.PrometheusRuleSpec) error {
	r.counter++
	return nil
}

func TestSyncLoop(t *testing.T) {
	rl := &testRulesLoader{}
	rs := &testRulesSyncer{}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(25*time.Second, func() { cancel() })

	SyncLoop(ctx, log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)), rl, rs, 5)

	// <-ctx.Done()
	testutil.Equals(t, 4, rs.counter)
}

func TestInitOrReloadObsctlConfig(t *testing.T) {
	o := &obsctlRulesSyncer{ctx: context.TODO(), logger: log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)), skipClientCheck: true}
	testutil.Ok(t, os.Setenv("OBSERVATORIUM_URL", "http://yolo.com/"))
	testutil.Ok(t, os.Setenv("OIDC_AUDIENCE", "test"))
	testutil.Ok(t, os.Setenv("OIDC_CLIENT_ID", "test"))
	testutil.Ok(t, os.Setenv("OIDC_CLIENT_SECRET", "test"))
	testutil.Ok(t, os.Setenv("OIDC_ISSUER_URL", "http://yolo-auth.com"))

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
			testutil.Ok(t, os.Setenv("MANAGED_TENANTS", tc.tenants))
			if tc.prexistingConfig != nil && len(tc.prexistingConfig.APIs["api"].Contexts) != 0 {
				testutil.Ok(t, tc.prexistingConfig.Save(o.logger))
				testutil.Ok(t, o.InitOrReloadObsctlConfig())
				testutil.Equals(t, tc.prexistingConfig.APIs, o.c.APIs)
				testutil.Equals(t, tc.prexistingConfig.Current, o.c.Current)
			} else {
				testutil.Ok(t, o.InitOrReloadObsctlConfig())
				testutil.Equals(t, tc.wantConfig, o.c)
			}
		})
	}
}

func TestGetTenantRuleGroups(t *testing.T) {
	k := &kubeRulesLoader{ctx: context.TODO(), logger: log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))}

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
			name:    "multiple tenant with multiple rulegroups",
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
			testutil.Ok(t, os.Setenv("MANAGED_TENANTS", tc.tenants))
			testutil.Equals(t, tc.want, k.GetTenantRuleGroups(tc.input))
		})
	}
}
