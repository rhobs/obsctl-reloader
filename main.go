package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
)

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

func main() {
	prometheusRules, _ := GetPrometheusRules()
	for _, pr := range prometheusRules {
		fmt.Println(pr.Name)
	}
}
