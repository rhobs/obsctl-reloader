package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/observatorium/api/client"
	"github.com/observatorium/api/client/parameters"
	"github.com/observatorium/obsctl/pkg/config"
	obsctlconfig "github.com/observatorium/obsctl/pkg/config"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
)

const obsctlContext = "api"

var logger log.Logger

func GetPrometheusRules() ([]monitoringv1.PrometheusRule, error) {
	ctx := context.Background()
	config := ctrl.GetConfigOrDie()
	dynamic := dynamic.NewForConfigOrDie(config)

	resourceId := schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "prometheusrules",
	}
	list, err := dynamic.Resource(resourceId).Namespace(os.Getenv("NAMESPACE_NAME")).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	prometheusRules := make([]monitoringv1.PrometheusRule, len(list.Items))
	for idx, item := range list.Items {
		obj := monitoringv1.PrometheusRule{}
		j, err := json.Marshal(item.Object)
		if err != nil {
			return nil, err
		}
		err = json.Unmarshal(j, &obj)
		if err != nil {
			return nil, err
		}
		prometheusRules[idx] = obj
	}

	return prometheusRules, nil
}

func GetTenantRules(prometheusRules []monitoringv1.PrometheusRule) map[string][]monitoringv1.RuleGroup {
	tenantRules := make(map[string][]monitoringv1.RuleGroup)
	for _, pr := range prometheusRules {
		level.Info(logger).Log("msg", "checking prometheus rule for tenant", "name", pr.Name)
		if tenant, ok := pr.Labels["tenant"]; ok {
			level.Info(logger).Log("msg", "checking prometheus rule tenant rules", "name", pr.Name, "tenant", tenant)
			tenantRules[tenant] = append(tenantRules[tenant], pr.Spec.Groups...)
		}
	}
	return tenantRules
}

func InitObsctlTenantConfig(ctx context.Context, tenant string) (obsctlconfig.TenantConfig, error) {
	api := os.Getenv("OBSERVATORIUM_URL")
	conf := &obsctlconfig.Config{}
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

func ObsctlMetricsSet(ctx context.Context, tenantConfig obsctlconfig.TenantConfig, rules []monitoringv1.RuleGroup) error {
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
	body, _ := json.Marshal(rules)
	resp, err := fc.SetRawRulesWithBodyWithResponse(ctx, parameters.Tenant(tenantConfig.Tenant), "application/yaml", bytes.NewReader(body))
	if err != nil {
		level.Error(logger).Log("msg", "getting response", "error", err)
		return err
	}
	fmt.Println(resp.StatusCode())

	return nil
}

func main() {
	logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	prometheusRules, _ := GetPrometheusRules()
	tenantRules := GetTenantRules(prometheusRules)
	for tenant, rules := range tenantRules {
		ctx := context.TODO()
		tenantConfig, _ := InitObsctlTenantConfig(ctx, tenant)
		ObsctlMetricsSet(ctx, tenantConfig, rules)
	}
}
