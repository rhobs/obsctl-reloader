# obsctl-reloader

This tool uses [obsctl](https://github.com/observatorium/obsctl) to sync rules from your cluster to Observatorium [Rules API](https://observatorium.io/docs/design/rules-api.md/) via prometheus-operator’s [PrometheusRule CRD](https://prometheus-operator.dev/docs/operator/design/#prometheusrule).

## Usage

The full usage documentation can be found in the [Red Hat Monitoring Group Handbook](https://rhobs-handbook.netlify.app/services/rhobs/rules-and-alerting.md/#sync-rules-from-your-cluster).

```bash mdox-exec="obsctl-reloader --help"
Usage of obsctl-reloader:
  -audience string
    	The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.
  -issuer-url string
    	The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.
  -kubeconfig string
    	Paths to a kubeconfig. Only required if out-of-cluster.
  -managed-tenants string
    	The name of the tenants whose rules should be synced. If there are multiple tenants, ensure they are comma-separated.
  -observatorium-api-url string
    	The URL of the Observatorium API to which rules will be synced.
  -sleep-duration-seconds uint
    	The interval in seconds after which all PrometheusRules are synced to Observatorium API. (default 15)
```

> Note: this project is still experimental.
