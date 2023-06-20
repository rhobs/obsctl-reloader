package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/efficientgo/core/errors"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	lokiv1beta1 "github.com/grafana/loki/operator/apis/loki/v1beta1"
	"github.com/metalmatze/signal/internalserver"
	"github.com/observatorium/api/client/parameters"
	"github.com/observatorium/obsctl/pkg/config"
	"github.com/observatorium/obsctl/pkg/fetcher"
	"github.com/oklog/run"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/prometheus/pkg/rulefmt"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	k8sconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	obsctlContextAPIName        = "api"
	defaultSleepDurationSeconds = 15
)

// tenantRulesLoader represents logic for loading and filtering PrometheusRule objects by tenants.
// Useful for testing without spinning up cluster.
type tenantRulesLoader interface {
	getLokiAlertingRules() ([]lokiv1.AlertingRule, error)
	getLokiRecordingRules() ([]lokiv1.RecordingRule, error)
	getTenantLogsAlertingRuleGroups(alertingRules []lokiv1.AlertingRule) map[string]lokiv1.AlertingRuleSpec
	getTenantLogsRecordingRuleGroups(recordingRules []lokiv1.RecordingRule) map[string]lokiv1.RecordingRuleSpec

	getPrometheusRules() ([]*monitoringv1.PrometheusRule, error)
	getTenantMetricsRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec
}

// kubeRulesLoader implements tenantRulesLoader interface.
type kubeRulesLoader struct {
	ctx            context.Context
	k8s            client.Client
	logger         log.Logger
	namespace      string
	managedTenants string

	promRuleFetches       prometheus.Counter
	promRuleFetchFailures prometheus.Counter
	lokiRuleFetches       *prometheus.CounterVec
	lokiRuleFetchFailures *prometheus.CounterVec
	lokiTenantRules       *prometheus.GaugeVec
	promTenantRules       *prometheus.GaugeVec
}

func newKubeRulesLoader(
	ctx context.Context,
	kc client.Client,
	logger log.Logger,
	namespace string,
	managedTenants string,
	reg prometheus.Registerer,
) *kubeRulesLoader {
	return &kubeRulesLoader{
		ctx:            ctx,
		k8s:            kc,
		logger:         logger,
		namespace:      namespace,
		managedTenants: managedTenants,

		promRuleFetches: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Name: "obsctl_reloader_prom_rule_fetches_total",
			Help: "Total number of list operations for monitoringv1 PrometheusRules.",
		}),
		promRuleFetchFailures: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Name: "obsctl_reloader_prom_rule_fetch_failures_total",
			Help: "Total number of failed list operations for monitoringv1 PrometheusRules.",
		}),
		lokiRuleFetches: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "obsctl_reloader_loki_rule_fetches_total",
			Help: "Total number of list operations for lokiv1/v1beta1 rules.",
		}, []string{"type"}),
		lokiRuleFetchFailures: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "obsctl_reloader_loki_rule_fetch_failures_total",
			Help: "Total number of failed list operations for lokiv1/v1beta1 rules.",
		}, []string{"type"}),

		lokiTenantRules: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "obsctl_reloader_loki_tenant_rulegroups",
			Help: "Number of Loki rules loaded per tenant.",
		}, []string{"type", "tenant"}),
		promTenantRules: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "obsctl_reloader_prom_tenant_rulegroups",
			Help: "Number of Prometheus rules loaded per tenant.",
		}, []string{"tenant"}),
	}
}

func (k *kubeRulesLoader) getLokiAlertingRules() ([]lokiv1.AlertingRule, error) {
	arV1Beta1 := lokiv1beta1.AlertingRuleList{}
	if err := k.k8s.List(k.ctx, &arV1Beta1, client.InNamespace(k.namespace)); err != nil {
		k.lokiRuleFetchFailures.WithLabelValues("alerting").Inc()
		return nil, errors.Wrap(err, "listing loki alerting rule v1beta1 objects")
	}

	arV1 := lokiv1.AlertingRuleList{}
	if err := k.k8s.List(k.ctx, &arV1, client.InNamespace(k.namespace)); err != nil {
		k.lokiRuleFetchFailures.WithLabelValues("alerting").Inc()
		return nil, errors.Wrap(err, "listing loki alerting rule v1 objects")
	}

	for _, ar := range arV1Beta1.Items {
		v1 := lokiv1.AlertingRule{}
		if err := ar.ConvertTo(&v1); err != nil {
			return nil, errors.Wrap(err, "converting loki v1beta1 to v1")
		}

		arV1.Items = append(arV1.Items, v1)
	}

	k.lokiRuleFetches.WithLabelValues("alerting").Inc()
	return arV1.Items, nil
}

func (k *kubeRulesLoader) getLokiRecordingRules() ([]lokiv1.RecordingRule, error) {
	rrV1Beta1 := lokiv1beta1.RecordingRuleList{}
	if err := k.k8s.List(k.ctx, &rrV1Beta1, client.InNamespace(k.namespace)); err != nil {
		k.lokiRuleFetchFailures.WithLabelValues("recording").Inc()
		return nil, errors.Wrap(err, "listing loki recording rule v1beta1 objects")
	}

	rrV1 := lokiv1.RecordingRuleList{}
	if err := k.k8s.List(k.ctx, &rrV1, client.InNamespace(k.namespace)); err != nil {
		k.lokiRuleFetchFailures.WithLabelValues("recording").Inc()
		return nil, errors.Wrap(err, "listing loki recording rule v1 objects")
	}

	for _, ar := range rrV1Beta1.Items {
		v1 := lokiv1.RecordingRule{}
		if err := ar.ConvertTo(&v1); err != nil {
			return nil, errors.Wrap(err, "converting loki v1beta1 to v1")
		}

		rrV1.Items = append(rrV1.Items, v1)
	}

	k.lokiRuleFetches.WithLabelValues("recording").Inc()
	return rrV1.Items, nil
}

func (k *kubeRulesLoader) getPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
	prometheusRules := monitoringv1.PrometheusRuleList{}
	err := k.k8s.List(k.ctx, &prometheusRules, client.InNamespace(k.namespace))
	if err != nil {
		k.promRuleFetchFailures.Inc()
		return nil, errors.Wrap(err, "listing prometheus rule objects")
	}

	k.promRuleFetches.Inc()
	return prometheusRules.Items, nil
}

func (k *kubeRulesLoader) getTenantLogsAlertingRuleGroups(alertingRules []lokiv1.AlertingRule) map[string]lokiv1.AlertingRuleSpec {
	tenantRules := make(map[string][]*lokiv1.AlertingRuleGroup)
	managedTenants := strings.Split(k.managedTenants, ",")
	for _, tenant := range managedTenants {
		if tenant != "" {
			tenantRules[tenant] = []*lokiv1.AlertingRuleGroup{}
		}
	}

	for _, ar := range alertingRules {
		level.Debug(k.logger).Log("msg", "checking Loki alerting rule for tenant", "name", ar.Name)
		if _, found := tenantRules[ar.Spec.TenantID]; !found {
			level.Debug(k.logger).Log("msg", "skipping Loki alerting rule with unmanaged tenant", "name", ar.Name, "tenant", ar.Spec.TenantID)
			continue
		}

		level.Debug(k.logger).Log("msg", "checking Loki alerting rule tenant rules", "name", ar.Name, "tenant", ar.Spec.TenantID)
		tenantRules[ar.Spec.TenantID] = append(tenantRules[ar.Spec.TenantID], ar.Spec.Groups...)
	}

	tenantRuleGroups := make(map[string]lokiv1.AlertingRuleSpec, len(tenantRules))
	for tenant, tr := range tenantRules {
		k.lokiTenantRules.WithLabelValues("alerting", tenant).Set(float64(len(tr)))
		tenantRuleGroups[tenant] = lokiv1.AlertingRuleSpec{Groups: tr}
	}

	return tenantRuleGroups
}

func (k *kubeRulesLoader) getTenantLogsRecordingRuleGroups(recordingRules []lokiv1.RecordingRule) map[string]lokiv1.RecordingRuleSpec {
	tenantRules := make(map[string][]*lokiv1.RecordingRuleGroup)
	managedTenants := strings.Split(k.managedTenants, ",")
	for _, tenant := range managedTenants {
		if tenant != "" {
			tenantRules[tenant] = []*lokiv1.RecordingRuleGroup{}
		}
	}

	for _, ar := range recordingRules {
		level.Debug(k.logger).Log("msg", "checking Loki Recording rule for tenant", "name", ar.Name)
		if _, found := tenantRules[ar.Spec.TenantID]; !found {
			level.Debug(k.logger).Log("msg", "skipping Loki Recording rule with unmanaged tenant", "name", ar.Name, "tenant", ar.Spec.TenantID)
			continue
		}

		level.Debug(k.logger).Log("msg", "checking Loki Recording rule tenant rules", "name", ar.Name, "tenant", ar.Spec.TenantID)
		tenantRules[ar.Spec.TenantID] = append(tenantRules[ar.Spec.TenantID], ar.Spec.Groups...)
	}

	tenantRuleGroups := make(map[string]lokiv1.RecordingRuleSpec, len(tenantRules))
	for tenant, tr := range tenantRules {
		k.lokiTenantRules.WithLabelValues("recording", tenant).Set(float64(len(tr)))
		tenantRuleGroups[tenant] = lokiv1.RecordingRuleSpec{Groups: tr}
	}

	return tenantRuleGroups
}

func (k *kubeRulesLoader) getTenantMetricsRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
	tenantRules := make(map[string][]monitoringv1.RuleGroup)
	managedTenants := strings.Split(k.managedTenants, ",")
	for _, tenant := range managedTenants {
		if tenant != "" {
			tenantRules[tenant] = []monitoringv1.RuleGroup{}
		}
	}

	for _, pr := range prometheusRules {
		level.Debug(k.logger).Log("msg", "checking prometheus rule for tenant", "name", pr.Name)
		if tenant, ok := pr.Labels["tenant"]; ok {
			if _, found := tenantRules[tenant]; !found {
				level.Debug(k.logger).Log("msg", "skipping prometheus rule with unmanaged tenant", "name", pr.Name, "tenant", tenant)
				continue
			}
			level.Debug(k.logger).Log("msg", "checking prometheus rule tenant rules", "name", pr.Name, "tenant", tenant)
			tenantRules[tenant] = append(tenantRules[tenant], pr.Spec.Groups...)
		} else {
			level.Debug(k.logger).Log("msg", "skipping prometheus rule without tenant label", "name", pr.Name)
		}
	}

	tenantRuleGroups := make(map[string]monitoringv1.PrometheusRuleSpec, len(tenantRules))
	for tenant, tr := range tenantRules {
		k.promTenantRules.WithLabelValues(tenant).Set(float64(len(tr)))
		tenantRuleGroups[tenant] = monitoringv1.PrometheusRuleSpec{Groups: tr}
	}

	return tenantRuleGroups
}

// tenantRulesSyncer implements logic for syncing rules to Observatorium API.
type tenantRulesSyncer interface {
	initOrReloadObsctlConfig() error
	setCurrentTenant(tenant string) error
	obsctlLogsAlertingSet(rules lokiv1.AlertingRuleSpec) error
	obsctlLogsRecordingSet(rules lokiv1.RecordingRuleSpec) error
	obsctlMetricsSet(rules monitoringv1.PrometheusRuleSpec) error
}

// obsctlRulesSyncer implements tenantRulesSyncer.
type obsctlRulesSyncer struct {
	ctx             context.Context
	logger          log.Logger
	skipClientCheck bool

	apiURL         string
	audience       string
	issuerURL      string
	managedTenants string

	c *config.Config

	lokiRulesSetOps      *prometheus.CounterVec
	promRulesSetOps      *prometheus.CounterVec
	lokiRulesSetFailures *prometheus.CounterVec
	promRulesSetFailures *prometheus.CounterVec
}

func newObsctlRulesSyncer(
	ctx context.Context,
	logger log.Logger,
	apiURL, audience, issuerURL, managedTenants string,
	reg prometheus.Registerer,
) *obsctlRulesSyncer {
	return &obsctlRulesSyncer{
		ctx:            ctx,
		logger:         logger,
		apiURL:         apiURL,
		audience:       audience,
		issuerURL:      issuerURL,
		managedTenants: managedTenants,

		lokiRulesSetOps: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "obsctl_reloader_loki_rule_sets_total",
			Help: "Total number of obsctl set operations for lokiv1/v1beta1 rules.",
		}, []string{"type", "tenant"}),
		promRulesSetOps: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "obsctl_reloader_prom_rule_sets_total",
			Help: "Total number of obsctl set operations for monitoringv1 rules.",
		}, []string{"tenant"}),
		lokiRulesSetFailures: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "obsctl_reloader_loki_rule_set_failures_total",
			Help: "Total number of failed obsctl set operations for lokiv1/v1beta1 rules.",
		}, []string{"type", "tenant"}),
		promRulesSetFailures: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "obsctl_reloader_prom_rule_set_failures_total",
			Help: "Total number of failed obsctl set operations for monitoringv1 rules.",
		}, []string{"tenant"}),
	}
}

// InitOrReloadObsctlConfig reads config from disk if present, or initializes one based on env vars.
func (o *obsctlRulesSyncer) initOrReloadObsctlConfig() error {
	// Check if config is already present on disk.
	cfg, err := config.Read(o.logger)
	if err != nil {
		return errors.Wrap(err, "reading obsctl config from disk")
	}

	if len(cfg.APIs[obsctlContextAPIName].Contexts) != 0 && cfg.APIs[obsctlContextAPIName].URL == o.apiURL {
		o.c = cfg
		level.Info(o.logger).Log("msg", "loading obsctl config from disk")
		return nil
	}

	level.Info(o.logger).Log("msg", "creating new obsctl config")

	// No previous config present,
	// Add API.
	o.c = &config.Config{}
	if err := o.c.AddAPI(o.logger, obsctlContextAPIName, o.apiURL); err != nil {
		level.Error(o.logger).Log("msg", "add api", "error", err)
		return errors.Wrap(err, "adding new API to obsctl config")
	}

	// Add all managed tenants under the API.
	for _, tenant := range strings.Split(o.managedTenants, ",") {
		tenantCfg := config.TenantConfig{OIDC: new(config.OIDCConfig)}
		tenantCfg.Tenant = tenant
		tenantCfg.OIDC.Audience = o.audience
		tenantCfg.OIDC.ClientID = os.Getenv(strings.ToUpper(tenant) + "_CLIENT_ID")
		tenantCfg.OIDC.ClientSecret = os.Getenv(strings.ToUpper(tenant) + "_CLIENT_SECRET")
		tenantCfg.OIDC.IssuerURL = o.issuerURL

		if !o.skipClientCheck {
			// We create a client here to check if config is valid for a particular managed tenant.
			if _, err := tenantCfg.Client(o.ctx, o.logger); err != nil {
				level.Error(o.logger).Log("msg", "creating authenticated client", "tenant", tenant, "error", err)
				// Don't block on this error. We can still sync rules for other tenants.
				continue
			}
		}

		if err := o.c.AddTenant(o.logger, tenantCfg.Tenant, obsctlContextAPIName, tenantCfg.Tenant, tenantCfg.OIDC); err != nil {
			level.Error(o.logger).Log("msg", "adding tenant", "tenant", tenant, "error", err)
			return errors.Wrap(err, "adding tenant to obsctl config")
		}
	}

	return nil
}

func (o *obsctlRulesSyncer) setCurrentTenant(tenant string) error {
	if err := o.c.SetCurrentContext(o.logger, obsctlContextAPIName, tenant); err != nil {
		level.Error(o.logger).Log("msg", "switching context", "tenant", tenant, "error", err)
		return err
	}

	return nil
}

func (o *obsctlRulesSyncer) obsctlLogsAlertingSet(rules lokiv1.AlertingRuleSpec) error {
	level.Debug(o.logger).Log("msg", "setting logs for tenant")
	fc, currentTenant, err := fetcher.NewCustomFetcher(o.ctx, o.logger)
	if err != nil {
		level.Error(o.logger).Log("msg", "getting fetcher client", "error", err)
		return errors.Wrap(err, "getting fetcher client")
	}

	for _, group := range rules.Groups {
		body, err := yaml.Marshal(group)
		if err != nil {
			level.Error(o.logger).Log("msg", "converting lokiv1 alerting rule group to yaml", "error", err)
			o.lokiRulesSetFailures.WithLabelValues("alerting", string(currentTenant)).Inc()
			return errors.Wrap(err, "converting lokiv1 alerting rule group to yaml")
		}

		level.Debug(o.logger).Log("msg", "setting rule file", "rule", string(body))
		resp, err := fc.SetLogsRulesWithBodyWithResponse(o.ctx, currentTenant, parameters.LogRulesNamespace(currentTenant), "application/yaml", bytes.NewReader(body))
		if err != nil {
			level.Error(o.logger).Log("msg", "getting response", "error", err)
			o.lokiRulesSetFailures.WithLabelValues("alerting", string(currentTenant)).Inc()
			return err
		}

		if resp.StatusCode()/100 != 2 {
			if len(resp.Body) != 0 {
				level.Error(o.logger).Log("msg", "setting loki alerting rules", "error", string(resp.Body))
				o.lokiRulesSetFailures.WithLabelValues("alerting", string(currentTenant)).Inc()
				return errors.Newf("non-200 status code: %v with body: %v", resp.StatusCode(), string(resp.Body))
			}
			o.lokiRulesSetFailures.WithLabelValues("alerting", string(currentTenant)).Inc()
			return errors.Newf("non-200 status code: %v with empty body", resp.StatusCode())
		}

		level.Debug(o.logger).Log("msg", string(resp.Body))
		o.lokiRulesSetOps.WithLabelValues("alerting", string(currentTenant)).Inc()
	}

	return nil
}

func (o *obsctlRulesSyncer) obsctlLogsRecordingSet(rules lokiv1.RecordingRuleSpec) error {
	level.Debug(o.logger).Log("msg", "setting logs for tenant")
	fc, currentTenant, err := fetcher.NewCustomFetcher(o.ctx, o.logger)
	if err != nil {
		level.Error(o.logger).Log("msg", "getting fetcher client", "error", err)
		return errors.Wrap(err, "getting fetcher client")
	}

	for _, group := range rules.Groups {
		body, err := yaml.Marshal(group)
		if err != nil {
			level.Error(o.logger).Log("msg", "converting lokiv1 recording rule group to yaml", "error", err)
			o.lokiRulesSetFailures.WithLabelValues("recording", string(currentTenant)).Inc()
			return errors.Wrap(err, "converting lokiv1 recording rule group to yaml")
		}

		level.Debug(o.logger).Log("msg", "setting rule file", "rule", string(body))
		resp, err := fc.SetLogsRulesWithBodyWithResponse(o.ctx, currentTenant, parameters.LogRulesNamespace(currentTenant), "application/yaml", bytes.NewReader(body))
		if err != nil {
			level.Error(o.logger).Log("msg", "getting response", "error", err)
			o.lokiRulesSetFailures.WithLabelValues("recording", string(currentTenant)).Inc()
			return err
		}

		if resp.StatusCode()/100 != 2 {
			if len(resp.Body) != 0 {
				level.Error(o.logger).Log("msg", "setting loki recording rules", "error", string(resp.Body))
				o.lokiRulesSetFailures.WithLabelValues("recording", string(currentTenant)).Inc()
				return errors.Newf("non-200 status code: %v with body: %v", resp.StatusCode(), string(resp.Body))
			}
			o.lokiRulesSetFailures.WithLabelValues("recording", string(currentTenant)).Inc()
			return errors.Newf("non-200 status code: %v with empty body", resp.StatusCode())
		}

		level.Debug(o.logger).Log("msg", string(resp.Body))
		o.lokiRulesSetOps.WithLabelValues("recording", string(currentTenant)).Inc()
	}

	return nil
}

func (o *obsctlRulesSyncer) obsctlMetricsSet(rules monitoringv1.PrometheusRuleSpec) error {
	level.Debug(o.logger).Log("msg", "setting metrics for tenant")
	fc, currentTenant, err := fetcher.NewCustomFetcher(o.ctx, o.logger)
	if err != nil {
		level.Error(o.logger).Log("msg", "getting fetcher client", "error", err)
		return errors.Wrap(err, "getting fetcher client")
	}

	ruleGroups, err := json.Marshal(rules)
	if err != nil {
		level.Error(o.logger).Log("msg", "converting monitoringv1 rules to json", "error", err)
		o.promRulesSetFailures.WithLabelValues(string(currentTenant)).Inc()
		return errors.Wrap(err, "converting monitoringv1 rules to json")
	}

	groups, errs := rulefmt.Parse(ruleGroups)
	if errs != nil || groups == nil {
		for e := range errs {
			level.Error(o.logger).Log("msg", "rulefmt parsing rules", "error", e, "groups", groups)
		}
		o.promRulesSetFailures.WithLabelValues(string(currentTenant)).Inc()
		return errors.Wrap(errs[0], "rulefmt parsing rules")
	}

	body, err := yaml.Marshal(groups)
	if err != nil {
		level.Error(o.logger).Log("msg", "converting rulefmt rules to yaml", "error", err)
		o.promRulesSetFailures.WithLabelValues(string(currentTenant)).Inc()
		return errors.Wrap(err, "converting rulefmt rules to yaml")
	}

	level.Debug(o.logger).Log("msg", "setting rule file", "rule", string(body))
	resp, err := fc.SetRawRulesWithBodyWithResponse(o.ctx, currentTenant, "application/yaml", bytes.NewReader(body))
	if err != nil {
		level.Error(o.logger).Log("msg", "getting response", "error", err)
		o.promRulesSetFailures.WithLabelValues(string(currentTenant)).Inc()
		return err
	}

	if resp.StatusCode()/100 != 2 {
		if len(resp.Body) != 0 {
			level.Error(o.logger).Log("msg", "setting rules", "error", string(resp.Body))
			o.promRulesSetFailures.WithLabelValues(string(currentTenant)).Inc()
			return errors.Newf("non-200 status code: %v with body: %v", resp.StatusCode(), string(resp.Body))
		}
		o.promRulesSetFailures.WithLabelValues(string(currentTenant)).Inc()
		return errors.Newf("non-200 status code: %v with empty body", resp.StatusCode())
	}

	o.promRulesSetOps.WithLabelValues(string(currentTenant)).Inc()
	level.Debug(o.logger).Log("msg", string(resp.Body))

	return nil
}

// syncLoop syncs PrometheusRule and Loki's AlertingRule/RecordingRule objects of each managed tenant with Observatorium API every SLEEP_DURATION_SECONDS.
func syncLoop(
	ctx context.Context,
	logger log.Logger,
	k tenantRulesLoader,
	o tenantRulesSyncer,
	logRulesEnabled bool,
	sleepDurationSeconds uint,
) error {
	for {
		select {
		case <-time.After(time.Duration(sleepDurationSeconds) * time.Second):
			prometheusRules, err := k.getPrometheusRules()
			if err != nil {
				level.Error(logger).Log("msg", "error getting prometheus rules", "error", err, "rules", len(prometheusRules))
				return err
			}

			// Set each tenant as current and set rules.
			for tenant, ruleGroups := range k.getTenantMetricsRuleGroups(prometheusRules) {
				if err := o.setCurrentTenant(tenant); err != nil {
					level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
					continue
				}

				err = o.obsctlMetricsSet(ruleGroups)
				if err != nil {
					level.Error(logger).Log("msg", "error setting rules", "tenant", tenant, "error", err)
					continue
				}
			}

			if logRulesEnabled {
				lokiAlertingRules, err := k.getLokiAlertingRules()
				if err != nil {
					level.Error(logger).Log("msg", "error getting loki alerting rules", "error", err, "rules", len(lokiAlertingRules))
					return err
				}

				for tenant, ruleGroups := range k.getTenantLogsAlertingRuleGroups(lokiAlertingRules) {
					if err := o.setCurrentTenant(tenant); err != nil {
						level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
						continue
					}

					err = o.obsctlLogsAlertingSet(ruleGroups)
					if err != nil {
						level.Error(logger).Log("msg", "error setting loki alerting rules", "tenant", tenant, "error", err)
						continue
					}
				}

				lokiRecordingRules, err := k.getLokiRecordingRules()
				if err != nil {
					level.Error(logger).Log("msg", "error getting loki recording rules", "error", err, "rules", len(lokiRecordingRules))
					return err
				}

				for tenant, ruleGroups := range k.getTenantLogsRecordingRuleGroups(lokiRecordingRules) {
					if err := o.setCurrentTenant(tenant); err != nil {
						level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
						continue
					}

					err = o.obsctlLogsRecordingSet(ruleGroups)
					if err != nil {
						level.Error(logger).Log("msg", "error setting loki recording rules", "tenant", tenant, "error", err)
						continue
					}
				}
			}

			level.Debug(logger).Log("msg", "sleeping", "duration", sleepDurationSeconds)
		case <-ctx.Done():
			return nil
		}
	}
}

type cfg struct {
	observatoriumURL     string
	sleepDurationSeconds uint
	managedTenants       string
	audience             string
	issuerURL            string
	logRulesEnabled      bool
	logLevel             string
	listenInternal       string
}

func setupLogger(logLevel string) log.Logger {
	var lvl level.Option
	switch logLevel {
	case "error":
		lvl = level.AllowError()
	case "warn":
		lvl = level.AllowWarn()
	case "info":
		lvl = level.AllowInfo()
	case "debug":
		lvl = level.AllowDebug()
	default:
		panic("unexpected log level")
	}

	logger := level.NewFilter(log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)), lvl)

	logger = log.With(logger, "name", "obsctl-reloader")
	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
	return logger
}

func parseFlags() *cfg {
	cfg := &cfg{}

	// Common flags.
	flag.UintVar(&cfg.sleepDurationSeconds, "sleep-duration-seconds", defaultSleepDurationSeconds, "The interval in seconds after which all PrometheusRules are synced to Observatorium API.")
	flag.StringVar(&cfg.observatoriumURL, "observatorium-api-url", "", "The URL of the Observatorium API to which rules will be synced.")
	flag.StringVar(&cfg.managedTenants, "managed-tenants", "", "The name of the tenants whose rules should be synced. If there are multiple tenants, ensure they are comma-separated.")
	flag.StringVar(&cfg.issuerURL, "issuer-url", "", "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	flag.StringVar(&cfg.audience, "audience", "", "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")
	flag.BoolVar(&cfg.logRulesEnabled, "log-rules-enabled", true, "Enable syncing Loki logging rules. Always on by default.")

	flag.StringVar(&cfg.logLevel, "log.level", "info", "Log filtering level. One of: debug, info, warn, error.")
	flag.StringVar(&cfg.listenInternal, "web.internal.listen", ":8081", "The address on which the internal server listens.")

	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()

	ctx, cancel := context.WithCancel(context.Background())

	namespace := os.Getenv("NAMESPACE_NAME")
	if namespace == "" {
		panic("Missing env var NAMESPACE_NAME")
	}

	logger := setupLogger(cfg.logLevel)
	defer level.Info(logger).Log("msg", "exiting")

	// Create kubernetes client for deployments
	k8sCfg, err := k8sconfig.GetConfig()
	if err != nil {
		panic("Failed to read kubeconfig")
	}

	mapper, err := apiutil.NewDynamicRESTMapper(k8sCfg)
	if err != nil {
		panic("Failed to create new dynamic REST mapper")
	}

	err = monitoringv1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic("Failed to register monitoringv1 types to runtime scheme")
	}

	if cfg.logRulesEnabled {
		err = lokiv1beta1.AddToScheme(scheme.Scheme)
		if err != nil {
			panic("Failed to register lokiv1beta1 types to runtime scheme")
		}

		err = lokiv1.AddToScheme(scheme.Scheme)
		if err != nil {
			panic("Failed to register lokiv1 types to runtime scheme")
		}
	}

	opts := client.Options{Scheme: scheme.Scheme, Mapper: mapper}
	k8sClient, err := client.New(k8sCfg, opts)
	if err != nil {
		panic("Failed to create new k8s client")
	}

	// Create prometheus registry.
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		//nolint:exhaustivestruct
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// Initialize config.
	o := newObsctlRulesSyncer(
		ctx,
		log.With(logger, "component", "obsctl-syncer"),
		cfg.observatoriumURL,
		cfg.audience,
		cfg.issuerURL,
		cfg.managedTenants,
		reg,
	)
	if err := o.initOrReloadObsctlConfig(); err != nil {
		level.Error(logger).Log("msg", "error reloading/initializing obsctl config", "error", err)
		panic(err)
	}

	var g run.Group
	{
		g.Add(run.SignalHandler(ctx, os.Interrupt, syscall.SIGINT, syscall.SIGTERM))
	}
	{
		g.Add(func() error {
			level.Info(logger).Log("msg", "starting obsctl-reloader sync")
			return syncLoop(ctx, logger,
				newKubeRulesLoader(ctx, k8sClient, logger, namespace, cfg.managedTenants, reg),
				o,
				cfg.logRulesEnabled,
				cfg.sleepDurationSeconds,
			)
		}, func(_ error) {
			cancel()
		})
	}
	{
		h := internalserver.NewHandler(
			internalserver.WithName("Internal - obsctl-reloader"),
			internalserver.WithPrometheusRegistry(reg),
			internalserver.WithPProf(),
		)

		//nolint:exhaustivestruct
		s := http.Server{
			Addr:    cfg.listenInternal,
			Handler: h,
		}

		g.Add(func() error {
			level.Info(logger).Log("msg", "starting internal HTTP server", "address", s.Addr)

			return s.ListenAndServe() //nolint:wrapcheck
		}, func(_ error) {
			_ = s.Shutdown(ctx)
			cancel()
		})
	}

	if err := g.Run(); err != nil {
		level.Error(logger).Log("msg", "starting run group", "error", err)
		os.Exit(1)
	}
}
