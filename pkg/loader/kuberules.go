package loader

import (
	"context"
	"strings"

	"github.com/efficientgo/core/errors"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	lokiv1beta1 "github.com/grafana/loki/operator/apis/loki/v1beta1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ RulesLoader = &KubeRulesLoader{}

// KubeRulesLoader implements RulesLoader interface.
type KubeRulesLoader struct {
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

func NewKubeRulesLoader(
	ctx context.Context,
	kc client.Client,
	logger log.Logger,
	namespace string,
	managedTenants string,
	reg prometheus.Registerer,
) *KubeRulesLoader {
	return &KubeRulesLoader{
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

func (k *KubeRulesLoader) GetLokiAlertingRules() ([]lokiv1.AlertingRule, error) {
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

func (k *KubeRulesLoader) GetLokiRecordingRules() ([]lokiv1.RecordingRule, error) {
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

func (k *KubeRulesLoader) GetPrometheusRules() ([]*monitoringv1.PrometheusRule, error) {
	prometheusRules := monitoringv1.PrometheusRuleList{}
	err := k.k8s.List(k.ctx, &prometheusRules, client.InNamespace(k.namespace))
	if err != nil {
		k.promRuleFetchFailures.Inc()
		return nil, errors.Wrap(err, "listing prometheus rule objects")
	}

	k.promRuleFetches.Inc()
	return prometheusRules.Items, nil
}

func (k *KubeRulesLoader) GetTenantLogsAlertingRuleGroups(alertingRules []lokiv1.AlertingRule) map[string]lokiv1.AlertingRuleSpec {
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

func (k *KubeRulesLoader) GetTenantLogsRecordingRuleGroups(recordingRules []lokiv1.RecordingRule) map[string]lokiv1.RecordingRuleSpec {
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

func (k *KubeRulesLoader) GetTenantMetricsRuleGroups(prometheusRules []*monitoringv1.PrometheusRule) map[string]monitoringv1.PrometheusRuleSpec {
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
