package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	lokiv1beta1 "github.com/grafana/loki/operator/apis/loki/v1beta1"
	"github.com/observatorium/api/client/parameters"
	"github.com/observatorium/obsctl/pkg/config"
	"github.com/observatorium/obsctl/pkg/fetcher"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/prometheus/pkg/rulefmt"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
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
	getLokiAlertingRules() ([]lokiv1beta1.AlertingRule, error)
	getLokiRecordingRules() ([]lokiv1beta1.RecordingRule, error)
	getPrometheusRules() ([]*monitoringv1.PrometheusRule, error)
	getTenantLogsAlertingRuleGroups(alertingRules []lokiv1beta1.AlertingRule) map[string]lokiv1beta1.AlertingRuleSpec
	getTenantLogsRecordingRuleGroups(recordingRules []lokiv1beta1.RecordingRule) map[string]lokiv1beta1.RecordingRuleSpec
	getTenantMetricsRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec
}

// kubeRulesLoader implements tenantRulesLoader interface.
type kubeRulesLoader struct {
	ctx            context.Context
	k8s            client.Client
	logger         log.Logger
	namespace      string
	managedTenants string
}

func (k *kubeRulesLoader) getLokiAlertingRules() ([]lokiv1beta1.AlertingRule, error) {
	alertingRules := lokiv1beta1.AlertingRuleList{}
	err := k.k8s.List(k.ctx, &alertingRules, client.InNamespace(k.namespace))
	if err != nil {
		return nil, err
	}

	return alertingRules.Items, nil
}

func (k *kubeRulesLoader) getLokiRecordingRules() ([]lokiv1beta1.RecordingRule, error) {
	recordingRules := lokiv1beta1.RecordingRuleList{}
	err := k.k8s.List(k.ctx, &recordingRules, client.InNamespace(k.namespace))
	if err != nil {
		return nil, err
	}

	return recordingRules.Items, nil
}

func (k *kubeRulesLoader) getPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
	prometheusRules := monitoringv1.PrometheusRuleList{}
	err := k.k8s.List(k.ctx, &prometheusRules, client.InNamespace(k.namespace))
	if err != nil {
		return nil, err
	}

	return prometheusRules.Items, nil
}

func (k *kubeRulesLoader) getTenantLogsAlertingRuleGroups(alertingRules []lokiv1beta1.AlertingRule) map[string]lokiv1beta1.AlertingRuleSpec {
	tenantRules := make(map[string][]*lokiv1beta1.AlertingRuleGroup)
	managedTenants := strings.Split(k.managedTenants, ",")
	for _, tenant := range managedTenants {
		if tenant != "" {
			tenantRules[tenant] = []*lokiv1beta1.AlertingRuleGroup{}
		}
	}

	for _, ar := range alertingRules {
		level.Info(k.logger).Log("msg", "checking Loki alerting rule for tenant", "name", ar.Name)
		if _, found := tenantRules[ar.Spec.TenantID]; !found {
			level.Info(k.logger).Log("msg", "skipping Loki alerting rule with unmanaged tenant", "name", ar.Name, "tenant", ar.Spec.TenantID)
			continue
		}

		level.Info(k.logger).Log("msg", "checking Loki alerting rule tenant rules", "name", ar.Name, "tenant", ar.Spec.TenantID)
		tenantRules[ar.Spec.TenantID] = append(tenantRules[ar.Spec.TenantID], ar.Spec.Groups...)
	}

	tenantRuleGroups := make(map[string]lokiv1beta1.AlertingRuleSpec, len(tenantRules))
	for tenant, tr := range tenantRules {
		tenantRuleGroups[tenant] = lokiv1beta1.AlertingRuleSpec{Groups: tr}
	}

	return tenantRuleGroups
}

func (k *kubeRulesLoader) getTenantLogsRecordingRuleGroups(recordingRules []lokiv1beta1.RecordingRule) map[string]lokiv1beta1.RecordingRuleSpec {
	tenantRules := make(map[string][]*lokiv1beta1.RecordingRuleGroup)
	managedTenants := strings.Split(k.managedTenants, ",")
	for _, tenant := range managedTenants {
		if tenant != "" {
			tenantRules[tenant] = []*lokiv1beta1.RecordingRuleGroup{}
		}
	}

	for _, ar := range recordingRules {
		level.Info(k.logger).Log("msg", "checking Loki Recording rule for tenant", "name", ar.Name)
		if _, found := tenantRules[ar.Spec.TenantID]; !found {
			level.Info(k.logger).Log("msg", "skipping Loki Recording rule with unmanaged tenant", "name", ar.Name, "tenant", ar.Spec.TenantID)
			continue
		}

		level.Info(k.logger).Log("msg", "checking Loki Recording rule tenant rules", "name", ar.Name, "tenant", ar.Spec.TenantID)
		tenantRules[ar.Spec.TenantID] = append(tenantRules[ar.Spec.TenantID], ar.Spec.Groups...)
	}

	tenantRuleGroups := make(map[string]lokiv1beta1.RecordingRuleSpec, len(tenantRules))
	for tenant, tr := range tenantRules {
		tenantRuleGroups[tenant] = lokiv1beta1.RecordingRuleSpec{Groups: tr}
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
		level.Info(k.logger).Log("msg", "checking prometheus rule for tenant", "name", pr.Name)
		if tenant, ok := pr.Labels["tenant"]; ok {
			if _, found := tenantRules[tenant]; !found {
				level.Info(k.logger).Log("msg", "skipping prometheus rule with unmanaged tenant", "name", pr.Name, "tenant", tenant)
				continue
			}
			level.Info(k.logger).Log("msg", "checking prometheus rule tenant rules", "name", pr.Name, "tenant", tenant)
			tenantRules[tenant] = append(tenantRules[tenant], pr.Spec.Groups...)
		} else {
			level.Info(k.logger).Log("msg", "skipping prometheus rule without tenant label", "name", pr.Name)
		}
	}

	tenantRuleGroups := make(map[string]monitoringv1.PrometheusRuleSpec, len(tenantRules))
	for tenant, tr := range tenantRules {
		tenantRuleGroups[tenant] = monitoringv1.PrometheusRuleSpec{Groups: tr}
	}

	return tenantRuleGroups
}

// tenantRulesSyncer implements logic for syncing rules to Observatorium API.
type tenantRulesSyncer interface {
	initOrReloadObsctlConfig() error
	setCurrentTenant(tenant string) error
	obsctlLogsAlertingSet(rules lokiv1beta1.AlertingRuleSpec) error
	obsctlLogsRecordingSet(rules lokiv1beta1.RecordingRuleSpec) error
	obsctlMetricsSet(rules monitoringv1.PrometheusRuleSpec) error
}

// obsctlRulesSyncer implements tenantRulesSyncer.
type obsctlRulesSyncer struct {
	ctx             context.Context
	k8s             client.Client
	namespace       string
	logger          log.Logger
	skipClientCheck bool

	apiURL         string
	audience       string
	issuerURL      string
	managedTenants string

	c *config.Config
}

// InitOrReloadObsctlConfig reads config from disk if present, or initializes one based on env vars.
func (o *obsctlRulesSyncer) initOrReloadObsctlConfig() error {
	// Check if config is already present on disk.
	cfg, err := config.Read(o.logger)
	if err != nil {
		return err
	}

	if len(cfg.APIs[obsctlContextAPIName].Contexts) != 0 && cfg.APIs[obsctlContextAPIName].URL == o.apiURL {
		o.c = cfg
		level.Info(o.logger).Log("msg", "loading config from disk")
		return nil
	}

	level.Info(o.logger).Log("msg", "creating new config")

	// No previous config present,
	// Add API.
	o.c = &config.Config{}
	if err := o.c.AddAPI(o.logger, obsctlContextAPIName, o.apiURL); err != nil {
		level.Error(o.logger).Log("msg", "add api", "error", err)
		return err
	}

	tenantSecret := map[string]corev1.Secret{}

	// List secrets.
	secret := corev1.SecretList{}
	err = o.k8s.List(o.ctx, &secret, client.InNamespace(o.namespace))
	if err != nil {
		return err
	}

	// Filter secrets for configured tenants.
	configuredTenants := strings.Split(o.managedTenants, ",")
	for i := range secret.Items {
		lbls := secret.Items[i].Labels

		for _, tenant := range configuredTenants {
			if t, ok := lbls["tenant"]; ok {
				if tenant == t {
					tenantSecret[tenant] = secret.Items[i]
					break
				}
			}
		}
	}

	// Add all managed tenants under the API.
	for tenant, secret := range tenantSecret {
		tenantCfg := config.TenantConfig{OIDC: new(config.OIDCConfig)}
		tenantCfg.Tenant = tenant
		tenantCfg.OIDC.Audience = o.audience
		// Get tenant credentials from secret.
		if secret.Data != nil {
			tenantCfg.OIDC.ClientID = string(secret.Data["client_id"])
			tenantCfg.OIDC.ClientSecret = string(secret.Data["client_secret"])
		}
		tenantCfg.OIDC.IssuerURL = o.issuerURL

		if !o.skipClientCheck {
			// We create a client here to check if config is valid for a particular managed tenant.
			if _, err := tenantCfg.Client(o.ctx, o.logger); err != nil {
				level.Error(o.logger).Log("msg", "creating authenticated client", "tenant", tenant, "error", err)
				return err
			}
		}

		if err := o.c.AddTenant(o.logger, tenantCfg.Tenant, obsctlContextAPIName, tenantCfg.Tenant, tenantCfg.OIDC); err != nil {
			level.Error(o.logger).Log("msg", "adding tenant", "tenant", tenant, "error", err)
			return err
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

func (o *obsctlRulesSyncer) obsctlLogsAlertingSet(rules lokiv1beta1.AlertingRuleSpec) error {
	level.Info(o.logger).Log("msg", "setting logs for tenant")
	fc, currentTenant, err := fetcher.NewCustomFetcher(o.ctx, o.logger)
	if err != nil {
		level.Error(o.logger).Log("msg", "getting fetcher client", "error", err)
		return err
	}

	for _, group := range rules.Groups {
		body, err := yaml.Marshal(group)
		if err != nil {
			level.Error(o.logger).Log("msg", "converting lokiv1beta1 alerting rule group to yaml", "error", err)
			return err
		}

		level.Info(o.logger).Log("msg", "setting rule file", "rule", string(body))
		resp, err := fc.SetLogsRulesWithBodyWithResponse(o.ctx, currentTenant, parameters.LogRulesNamespace(currentTenant), "application/yaml", bytes.NewReader(body))
		if err != nil {
			level.Error(o.logger).Log("msg", "getting response", "error", err)
			return err
		}

		if resp.StatusCode()/100 != 2 {
			if len(resp.Body) != 0 {
				level.Error(o.logger).Log("msg", "setting loki alerting rules", "error", string(resp.Body))
				return err
			}
		}
		level.Info(o.logger).Log("msg", string(resp.Body))
	}

	return nil
}

func (o *obsctlRulesSyncer) obsctlLogsRecordingSet(rules lokiv1beta1.RecordingRuleSpec) error {
	level.Info(o.logger).Log("msg", "setting logs for tenant")
	fc, currentTenant, err := fetcher.NewCustomFetcher(o.ctx, o.logger)
	if err != nil {
		level.Error(o.logger).Log("msg", "getting fetcher client", "error", err)
		return err
	}

	for _, group := range rules.Groups {
		body, err := yaml.Marshal(group)
		if err != nil {
			level.Error(o.logger).Log("msg", "converting lokiv1beta1 recording rule group to yaml", "error", err)
			return err
		}

		level.Info(o.logger).Log("msg", "setting rule file", "rule", string(body))
		resp, err := fc.SetLogsRulesWithBodyWithResponse(o.ctx, currentTenant, parameters.LogRulesNamespace(currentTenant), "application/yaml", bytes.NewReader(body))
		if err != nil {
			level.Error(o.logger).Log("msg", "getting response", "error", err)
			return err
		}

		if resp.StatusCode()/100 != 2 {
			if len(resp.Body) != 0 {
				level.Error(o.logger).Log("msg", "setting loki recording rules", "error", string(resp.Body))
				return err
			}
		}
		level.Info(o.logger).Log("msg", string(resp.Body))
	}

	return nil
}

func (o *obsctlRulesSyncer) obsctlMetricsSet(rules monitoringv1.PrometheusRuleSpec) error {
	level.Info(o.logger).Log("msg", "setting metrics for tenant")
	fc, currentTenant, err := fetcher.NewCustomFetcher(o.ctx, o.logger)
	if err != nil {
		level.Error(o.logger).Log("msg", "getting fetcher client", "error", err)
		return err
	}

	ruleGroups, err := json.Marshal(rules)
	if err != nil {
		level.Error(o.logger).Log("msg", "converting monitoringv1 rules to json", "error", err)
		return err
	}

	groups, errs := rulefmt.Parse(ruleGroups)
	if errs != nil || groups == nil {
		for e := range errs {
			level.Error(o.logger).Log("msg", "rulefmt parsing rules", "error", e, "groups", groups)
		}
		return errs[0]
	}

	body, err := yaml.Marshal(groups)
	if err != nil {
		level.Error(o.logger).Log("msg", "converting rulefmt rules to yaml", "error", err)
		return err
	}

	level.Info(o.logger).Log("msg", "setting rule file", "rule", string(body))
	resp, err := fc.SetRawRulesWithBodyWithResponse(o.ctx, currentTenant, "application/yaml", bytes.NewReader(body))
	if err != nil {
		level.Error(o.logger).Log("msg", "getting response", "error", err)
		return err
	}

	if resp.StatusCode()/100 != 2 {
		if len(resp.Body) != 0 {
			level.Error(o.logger).Log("msg", "setting rules", "error", string(resp.Body))
			return err
		}
	}
	level.Info(o.logger).Log("msg", string(resp.Body))

	return nil
}

// syncLoop syncs PrometheusRule and Loki's AlertingRule/RecordingRule objects of each managed tenant with Observatorium API every SLEEP_DURATION_SECONDS.
func syncLoop(ctx context.Context, logger log.Logger, k tenantRulesLoader, o tenantRulesSyncer, sleepDurationSeconds uint) {
	for {
		select {
		case <-time.After(time.Duration(sleepDurationSeconds) * time.Second):
			prometheusRules, err := k.getPrometheusRules()
			if err != nil {
				level.Error(logger).Log("msg", "error getting prometheus rules", "error", err, "rules", len(prometheusRules))
				os.Exit(1)
			}

			// Set each tenant as current and set rules.
			for tenant, ruleGroups := range k.getTenantMetricsRuleGroups(prometheusRules) {
				if err := o.setCurrentTenant(tenant); err != nil {
					level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
					os.Exit(1)
				}

				err = o.obsctlMetricsSet(ruleGroups)
				if err != nil {
					level.Error(logger).Log("msg", "error setting rules", "tenant", tenant, "error", err)
					os.Exit(1)
				}
			}

			lokiAlertingRules, err := k.getLokiAlertingRules()
			if err != nil {
				level.Error(logger).Log("msg", "error getting loki alerting rules", "error", err, "rules", len(lokiAlertingRules))
				os.Exit(1)
			}

			for tenant, ruleGroups := range k.getTenantLogsAlertingRuleGroups(lokiAlertingRules) {
				if err := o.setCurrentTenant(tenant); err != nil {
					level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
					os.Exit(1)
				}

				err = o.obsctlLogsAlertingSet(ruleGroups)
				if err != nil {
					level.Error(logger).Log("msg", "error setting loki alerting rules", "tenant", tenant, "error", err)
					os.Exit(1)
				}
			}

			lokiRecordingRules, err := k.getLokiRecordingRules()
			if err != nil {
				level.Error(logger).Log("msg", "error getting loki recording rules", "error", err, "rules", len(lokiRecordingRules))
				os.Exit(1)
			}

			for tenant, ruleGroups := range k.getTenantLogsRecordingRuleGroups(lokiRecordingRules) {
				if err := o.setCurrentTenant(tenant); err != nil {
					level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
					os.Exit(1)
				}

				err = o.obsctlLogsRecordingSet(ruleGroups)
				if err != nil {
					level.Error(logger).Log("msg", "error setting loki recording rules", "tenant", tenant, "error", err)
					os.Exit(1)
				}
			}

			level.Info(logger).Log("msg", "sleeping", "duration", sleepDurationSeconds)
		case <-ctx.Done():
			return
		}
	}
}

type cfg struct {
	observatoriumURL     string
	sleepDurationSeconds uint
	managedTenants       string
	audience             string
	issuerURL            string
}

func parseFlags() *cfg {
	cfg := &cfg{}

	// Common flags.
	flag.UintVar(&cfg.sleepDurationSeconds, "sleep-duration-seconds", defaultSleepDurationSeconds, "The interval in seconds after which all PrometheusRules are synced to Observatorium API.")
	flag.StringVar(&cfg.observatoriumURL, "observatorium-api-url", "", "The URL of the Observatorium API to which rules will be synced.")
	flag.StringVar(&cfg.managedTenants, "managed-tenants", "", "The name of the tenants whose rules should be synced. If there are multiple tenants, ensure they are comma-separated.")
	flag.StringVar(&cfg.issuerURL, "issuer-url", "", "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	flag.StringVar(&cfg.audience, "audience", "", "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")

	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	namespace := os.Getenv("NAMESPACE_NAME")
	if namespace == "" {
		panic("Missing env var NAMESPACE_NAME")
	}

	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))

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

	err = lokiv1beta1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic("Failed to register lokiv1beta1 types to runtime scheme")
	}

	opts := client.Options{Scheme: scheme.Scheme, Mapper: mapper}

	k8sClient, err := client.New(k8sCfg, opts)
	if err != nil {
		panic("Failed to create new k8s client")
	}

	o := &obsctlRulesSyncer{
		ctx:            ctx,
		k8s:            k8sClient,
		namespace:      namespace,
		logger:         log.With(logger, "component", "obsctl-syncer"),
		apiURL:         cfg.observatoriumURL,
		audience:       cfg.audience,
		issuerURL:      cfg.issuerURL,
		managedTenants: cfg.managedTenants,
	}

	// Initialize config.
	if err := o.initOrReloadObsctlConfig(); err != nil {
		level.Error(logger).Log("msg", "error reloading/initializing obsctl config", "error", err)
		os.Exit(1)
	}

	syncLoop(ctx, logger,
		&kubeRulesLoader{
			ctx:            ctx,
			k8s:            k8sClient,
			logger:         log.With(logger, "component", "kube-rules-loader"),
			namespace:      namespace,
			managedTenants: cfg.managedTenants,
		}, o,
		cfg.sleepDurationSeconds,
	)
}
