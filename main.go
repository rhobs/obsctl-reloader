package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/go-kit/log"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
)

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
		logger.Log("action", "checking prometheus rule for tenant", "name", pr.Name)
		if tenant, ok := pr.Labels["tenant"]; ok {
			logger.Log("action", "checking prometheus rule tenant rules", "name", pr.Name, "tenant", tenant)
			tenantRules[tenant] = append(tenantRules[tenant], pr.Spec.Groups...)
		}
	}
	return tenantRules
}

func main() {
	logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	prometheusRules, _ := GetPrometheusRules()
	tenantRules := GetTenantRules(prometheusRules)
	for tenant, rules := range tenantRules {
		fmt.Println(tenant)
		fmt.Println(rules)
	}
}
