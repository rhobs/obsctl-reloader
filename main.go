package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/observatorium/obsctl/pkg/config"
	"github.com/observatorium/obsctl/pkg/fetcher"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const obsctlContextAPIName = "api"
const defaultSleepDurationSeconds = 30

// tenantRulesLoader represents logic for loading and filtering PrometheusRule objects by tenants.
// Useful for testing without spinning up cluster.
type tenantRulesLoader interface {
	GetPrometheusRules() ([]*monitoringv1.PrometheusRule, error)
	GetTenantRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec
}

// kubeRulesLoader implements tenantRulesLoader interface.
type kubeRulesLoader struct {
	ctx    context.Context
	logger log.Logger
}

func (k *kubeRulesLoader) GetPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
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

func (k *kubeRulesLoader) GetTenantRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
	tenantRules := make(map[string][]monitoringv1.RuleGroup)
	managedTenants := strings.Split(os.Getenv("MANAGED_TENANTS"), ",")
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
	InitOrReloadObsctlConfig() error
	SetCurrentTenant(tenant string) error
	ObsctlMetricsSet(rules monitoringv1.PrometheusRuleSpec) error
}

// obsctlRulesSyncer implements tenantRulesSyncer.
type obsctlRulesSyncer struct {
	ctx             context.Context
	logger          log.Logger
	skipClientCheck bool

	c *config.Config
}

// InitOrReloadObsctlConfig reads config from disk if present, or initializes one based on env vars.
func (o *obsctlRulesSyncer) InitOrReloadObsctlConfig() error {
	// Check if config is already present on disk.
	cfg, err := config.Read(o.logger)
	if err != nil {
		return err
	}

	if len(cfg.APIs[obsctlContextAPIName].Contexts) != 0 && cfg.APIs[obsctlContextAPIName].URL == os.Getenv("OBSERVATORIUM_URL") {
		o.c = cfg
		level.Info(o.logger).Log("msg", "loading config from disk")
		return nil
	}

	level.Info(o.logger).Log("msg", "creating new config")

	// No previous config present,
	// Add API.
	api := os.Getenv("OBSERVATORIUM_URL")
	o.c = &config.Config{}
	if err := o.c.AddAPI(o.logger, obsctlContextAPIName, api); err != nil {
		level.Error(o.logger).Log("msg", "add api", "error", err)
		return err
	}

	// Add all managed tenants under the API.
	for _, tenant := range strings.Split(os.Getenv("MANAGED_TENANTS"), ",") {
		tenantCfg := config.TenantConfig{OIDC: new(config.OIDCConfig)}
		tenantCfg.Tenant = tenant
		tenantCfg.OIDC.Audience = os.Getenv("OIDC_AUDIENCE")
		tenantCfg.OIDC.ClientID = os.Getenv("OIDC_CLIENT_ID")
		tenantCfg.OIDC.ClientSecret = os.Getenv("OIDC_CLIENT_SECRET")
		tenantCfg.OIDC.IssuerURL = os.Getenv("OIDC_ISSUER_URL")

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

func (o *obsctlRulesSyncer) SetCurrentTenant(tenant string) error {
	if err := o.c.SetCurrentContext(o.logger, obsctlContextAPIName, tenant); err != nil {
		level.Error(o.logger).Log("msg", "switching context", "tenant", tenant, "error", err)
		return err
	}

	return nil
}

func (o *obsctlRulesSyncer) ObsctlMetricsSet(rules monitoringv1.PrometheusRuleSpec) error {
	level.Info(o.logger).Log("msg", "setting metrics for tenant")
	fc, currentTenant, err := fetcher.NewCustomFetcher(o.ctx, o.logger)
	if err != nil {
		level.Error(o.logger).Log("msg", "getting fetcher client", "error", err)
		return err
	}

	body, err := json.Marshal(rules)
	if err != nil {
		level.Error(o.logger).Log("msg", "converting rules to json", "error", err)
		return err
	}

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

// SyncLoop syncs PrometheusRule objects of each managed tenant with Observatorium API every SLEEP_DURATION_SECONDS.
func SyncLoop(ctx context.Context, logger log.Logger, k tenantRulesLoader, o tenantRulesSyncer, sleepDurationSeconds int) {
	for {
		select {
		case <-time.After(time.Duration(sleepDurationSeconds) * time.Second):
			prometheusRules, err := k.GetPrometheusRules()
			if err != nil {
				level.Error(logger).Log("msg", "error getting prometheus rules", "error", err, "rules", len(prometheusRules))
				os.Exit(1)
			}

			// Set each tenant as current and set rules.
			for tenant, ruleGroups := range k.GetTenantRuleGroups(prometheusRules) {
				if err := o.SetCurrentTenant(tenant); err != nil {
					level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
					os.Exit(1)
				}

				err = o.ObsctlMetricsSet(ruleGroups)
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

func main() {
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
		ctx:    ctx,
		logger: log.With(logger, "component", "obsctl-syncer"),
	}

	// Initialize config.
	if err := o.InitOrReloadObsctlConfig(); err != nil {
		level.Error(logger).Log("msg", "error reloading/initializing obsctl config", "error", err)
		os.Exit(1)
	}

	sleepDurationSeconds := defaultSleepDurationSeconds
	if value, ok := os.LookupEnv("SLEEP_DURATION_SECONDS"); ok {
		sleepDurationSeconds, _ = strconv.Atoi(value)
	}

	SyncLoop(ctx, logger,
		&kubeRulesLoader{
			ctx:    ctx,
			logger: log.With(logger, "component", "kube-rules-loader"),
		}, o,
		sleepDurationSeconds,
	)
}
