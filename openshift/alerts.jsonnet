(import '../jsonnet/lib/alerts.libsonnet') {
  _config+:: {
    obsctlReloaderSelector: 'job="obsctl-reloader"',
  }
}.prometheusAlerts 
