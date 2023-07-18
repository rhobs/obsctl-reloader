package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/efficientgo/core/errors"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	"github.com/observatorium/api/client/parameters"
	"github.com/observatorium/obsctl/pkg/config"
	"github.com/observatorium/obsctl/pkg/fetcher"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/prometheus/pkg/rulefmt"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	obsctlContextAPIName = "api"
)

var _ RulesSyncer = &ObsctlRulesSyncer{}

// ObsctlRulesSyncer implements RulesSyncer interface to sync rules to Observatorium API.
type ObsctlRulesSyncer struct {
	ctx             context.Context
	logger          log.Logger
	skipClientCheck bool
	k8s             client.Client
	namespace       string

	apiURL         string
	audience       string
	issuerURL      string
	managedTenants string

	autoDetectSecretsFn func(ctx context.Context,
		k8s client.Client,
		namespace, audience, issuerURL, managedTenants string,
	) (map[string]*config.OIDCConfig, error)

	c *config.Config

	lokiRulesSetOps      *prometheus.CounterVec
	promRulesSetOps      *prometheus.CounterVec
	lokiRulesSetFailures *prometheus.CounterVec
	promRulesSetFailures *prometheus.CounterVec
}

func NewObsctlRulesSyncer(
	ctx context.Context,
	logger log.Logger,
	kc client.Client,
	namespace, apiURL, audience, issuerURL, managedTenants string,
	reg prometheus.Registerer,
) *ObsctlRulesSyncer {
	return &ObsctlRulesSyncer{
		ctx:            ctx,
		logger:         logger,
		k8s:            kc,
		apiURL:         apiURL,
		namespace:      namespace,
		audience:       audience,
		issuerURL:      issuerURL,
		managedTenants: managedTenants,

		autoDetectSecretsFn: AutoDetectTenantSecrets,

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

func AutoDetectTenantSecrets(
	ctx context.Context,
	k8s client.Client,
	namespace, audience, issuerURL, managedTenants string,
) (map[string]*config.OIDCConfig, error) {
	tenantSecret := map[string]*config.OIDCConfig{}

	// List secrets by filtered with tenant label.
	ls, err := metav1.LabelSelectorAsSelector(
		&metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{{
				Key:      "tenant",
				Operator: metav1.LabelSelectorOpExists,
			}},
		})
	if err != nil {
		return nil, err
	}

	secret := corev1.SecretList{}
	if err := k8s.List(ctx, &secret, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: ls}); err != nil {
		return nil, err
	}

	// Filter secrets for configured tenants.
	configuredTenants := strings.Split(managedTenants, ",")
	for i := range secret.Items {
		lbls := secret.Items[i].Labels

		if _, ok := lbls["tenant"]; !ok {
			continue
		}

		// If tenant is not configured, skip.
		if !slices.Contains(configuredTenants, lbls["tenant"]) {
			continue
		}

		if secret.Items[i].Data == nil {
			continue
		}

		tOIDC := &config.OIDCConfig{
			Audience:      audience,
			IssuerURL:     issuerURL,
			OfflineAccess: false,
		}

		// Get tenant credentials from secret.
		// TODO: Define spec for secrets. Currently can be both underscore and dash.
		if cd, ok := secret.Items[i].Data["client_id"]; ok {
			tOIDC.ClientID = string(cd)
		}
		if cd, ok := secret.Items[i].Data["client-id"]; ok {
			tOIDC.ClientID = string(cd)
		}
		if cs, ok := secret.Items[i].Data["client_secret"]; ok {
			tOIDC.ClientSecret = string(cs)
		}
		if cs, ok := secret.Items[i].Data["client-secret"]; ok {
			tOIDC.ClientSecret = string(cs)
		}

		// Skip if secret is missing credentials.
		if tOIDC.ClientSecret == "" || tOIDC.ClientID == "" {
			continue
		}

		tenantSecret[lbls["tenant"]] = tOIDC
	}

	return tenantSecret, nil
}

// InitOrReloadObsctlConfig reads config from disk if present, or initializes one based on env vars.
func (o *ObsctlRulesSyncer) InitOrReloadObsctlConfig() error {
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

	tenantSecrets, err := o.autoDetectSecretsFn(o.ctx, o.k8s, o.namespace, o.audience, o.issuerURL, o.managedTenants)
	if err != nil {
		level.Error(o.logger).Log("msg", "auto detecting tenant secrets", "error", err)
		return errors.Wrap(err, "auto detecting tenant secrets")
	}

	// Add all managed tenants under the API.
	for tenant, oidc := range tenantSecrets {
		tenantCfg := config.TenantConfig{OIDC: oidc}
		tenantCfg.Tenant = tenant

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

func (o *ObsctlRulesSyncer) SetCurrentTenant(tenant string) error {
	if err := o.c.SetCurrentContext(o.logger, obsctlContextAPIName, tenant); err != nil {
		level.Error(o.logger).Log("msg", "switching context", "tenant", tenant, "error", err)
		return err
	}

	return nil
}

func (o *ObsctlRulesSyncer) LogsAlertingSet(rules lokiv1.AlertingRuleSpec) error {
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

func (o *ObsctlRulesSyncer) LogsRecordingSet(rules lokiv1.RecordingRuleSpec) error {
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

func (o *ObsctlRulesSyncer) MetricsSet(rules monitoringv1.PrometheusRuleSpec) error {
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
