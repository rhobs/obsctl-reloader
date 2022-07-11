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
	"github.com/observatorium/api/client"
	"github.com/observatorium/api/client/parameters"
	"github.com/observatorium/obsctl/pkg/config"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"github.com/rhobs/obsctl-reloader/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const obsctlContext = "api"
const defaultSleepDurationSeconds = 30

var logger log.Logger

func GetPrometheusRules(ctx context.Context) ([]*monitoringv1.PrometheusRule, error) {
	client, err := monitoringclient.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		return nil, err
	}
	prometheusRules, err := client.MonitoringV1().PrometheusRules(os.Getenv("NAMESPACE_NAME")).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return prometheusRules.Items, nil
}

func GetTenantRules(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
	tenantRules := make(map[string][]monitoringv1.RuleGroup)
	managedTenants := strings.Split(os.Getenv("MANAGED_TENANTS"), ",")
	for _, tenant := range managedTenants {
		tenantRules[tenant] = []monitoringv1.RuleGroup{}
	}

	for _, pr := range prometheusRules {
		level.Info(logger).Log("msg", "checking prometheus rule for tenant", "name", pr.Name)
		if tenant, ok := pr.Labels["tenant"]; ok {
			if !utils.Contains(managedTenants, tenant) {
				level.Info(logger).Log("msg", "skipping prometheus rule with unmanaged tenant", "name", pr.Name, "tenant", tenant)
				continue
			}
			level.Info(logger).Log("msg", "checking prometheus rule tenant rules", "name", pr.Name, "tenant", tenant)
			tenantRules[tenant] = append(tenantRules[tenant], pr.Spec.Groups...)
		} else {
			level.Info(logger).Log("msg", "skipping prometheus rule without tenant label", "name", pr.Name)
		}
	}

	tenantRuleGroups := make(map[string]monitoringv1.PrometheusRuleSpec, len(tenantRules))
	for tenant, tr := range tenantRules {
		tenantRuleGroups[tenant] = monitoringv1.PrometheusRuleSpec{Groups: tr}
	}

	return tenantRuleGroups
}

func InitObsctlTenantConfig(ctx context.Context, tenant string) (config.TenantConfig, error) {
	api := os.Getenv("OBSERVATORIUM_URL")
	conf := &config.Config{}
	tenantCfg := config.TenantConfig{OIDC: new(config.OIDCConfig)}
	if err := conf.AddAPI(logger, obsctlContext, api); err != nil {
		level.Error(logger).Log("msg", "add api", "error", err)
		return tenantCfg, err
	}
	tenantCfg.Tenant = tenant
	tenantCfg.OIDC.Audience = os.Getenv("OIDC_AUDIENCE")
	tenantCfg.OIDC.ClientID = os.Getenv("OIDC_CLIENT_ID")
	tenantCfg.OIDC.ClientSecret = os.Getenv("OIDC_CLIENT_SECRET")
	tenantCfg.OIDC.IssuerURL = os.Getenv("OIDC_ISSUER_URL")
	if _, err := tenantCfg.Client(ctx, logger); err != nil {
		level.Error(logger).Log("msg", "creating authenticated client", "error", err)
		return tenantCfg, err
	}

	if err := conf.AddTenant(logger, tenantCfg.Tenant, obsctlContext, tenantCfg.Tenant, tenantCfg.OIDC); err != nil {
		level.Error(logger).Log("msg", "adding tenant", "error", err)
		return tenantCfg, err
	}

	return tenantCfg, nil
}

func ObsctlMetricsSet(ctx context.Context, tenantConfig config.TenantConfig, rules monitoringv1.PrometheusRuleSpec) error {
	level.Info(logger).Log("msg", "setting metrics for tenant", "tenant", tenantConfig.Tenant)
	api := os.Getenv("OBSERVATORIUM_URL")
	c, err := tenantConfig.Client(ctx, logger)
	if err != nil {
		level.Error(logger).Log("msg", "getting client", "error", err)
		return err
	}
	fc, err := client.NewClientWithResponses(api, func(f *client.Client) error {
		f.Client = c
		return err
	})
	if err != nil {
		level.Error(logger).Log("msg", "getting fetcher client", "error", err)
		return err
	}
	body, err := json.Marshal(rules)
	if err != nil {
		level.Error(logger).Log("msg", "converting rules to json", "error", err)
		return err
	}
	resp, err := fc.SetRawRulesWithBodyWithResponse(ctx, parameters.Tenant(tenantConfig.Tenant), "application/yaml", bytes.NewReader(body))
	if err != nil {
		level.Error(logger).Log("msg", "getting response", "error", err)
		return err
	}
	if resp.StatusCode()/100 != 2 {
		if len(resp.Body) != 0 {
			level.Error(logger).Log("msg", "setting rules", "error", string(resp.Body))
			return err
		}
	}
	level.Info(logger).Log("msg", string(resp.Body))

	return nil
}

func Sleep() {
	sleepDurationSeconds := defaultSleepDurationSeconds
	if value, ok := os.LookupEnv("SLEEP_DURATION_SECONDS"); ok {
		sleepDurationSeconds, _ = strconv.Atoi(value)
	}
	level.Info(logger).Log("msg", "sleeping", "duration", sleepDurationSeconds)
	time.Sleep(time.Duration(sleepDurationSeconds) * time.Second)
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

	logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	for true {
		prometheusRules, err := GetPrometheusRules(ctx)
		if err != nil {
			level.Error(logger).Log("msg", "error getting prometheus rules")
			os.Exit(1)
		}
		tenantRules := GetTenantRules(prometheusRules)
		for tenant, rules := range tenantRules {
			tenantConfig, err := InitObsctlTenantConfig(ctx, tenant)
			if err != nil {
				level.Error(logger).Log("msg", "error initiating obsctl tenant config", "error", err)
				os.Exit(1)
			}
			err = ObsctlMetricsSet(ctx, tenantConfig, rules)
			if err != nil {
				level.Error(logger).Log("msg", "error setting rules", "error", err)
				os.Exit(1)
			}
		}
		Sleep()
	}
}
