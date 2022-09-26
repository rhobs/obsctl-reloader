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
	"github.com/observatorium/obsctl/pkg/config"
	"github.com/observatorium/obsctl/pkg/fetcher"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"github.com/prometheus/prometheus/pkg/rulefmt"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	obsctlContextAPIName        = "api"
	defaultSleepDurationSeconds = 15
)

// tenantRulesLoader represents logic for loading and filtering PrometheusRule objects by tenants.
// Useful for testing without spinning up cluster.
type tenantRulesLoader interface {
	getPrometheusRules() ([]*monitoringv1.PrometheusRule, error)
	getTenantRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec
}

// kubeRulesLoader implements tenantRulesLoader interface.
type kubeRulesLoader struct {
	ctx            context.Context
	logger         log.Logger
	managedTenants string
}

func (k *kubeRulesLoader) getPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
	client, err := monitoringclient.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		return nil, err
	}
	prometheusRules, err := client.MonitoringV1().PrometheusRules(os.Getenv("NAMESPACE_NAME")).
		List(k.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return prometheusRules.Items, nil
}

func (k *kubeRulesLoader) getTenantRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
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

// syncLoop syncs PrometheusRule objects of each managed tenant with Observatorium API every SLEEP_DURATION_SECONDS.
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
			for tenant, ruleGroups := range k.getTenantRuleGroups(prometheusRules) {
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

	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))

	o := &obsctlRulesSyncer{
		ctx:            ctx,
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
			logger:         log.With(logger, "component", "kube-rules-loader"),
			managedTenants: cfg.managedTenants,
		}, o,
		cfg.sleepDurationSeconds,
	)
}
