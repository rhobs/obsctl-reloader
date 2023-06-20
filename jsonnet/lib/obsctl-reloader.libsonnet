// These are the defaults for this components configuration.
// When calling the function to generate the component's manifest,
// you can pass an object structured like the default to overwrite default values.
local defaults = {
  local defaults = self,
  name: error 'must provide name',
  namespace: error 'must provide namespace',
  version: error 'must provide version',
  image: error 'must provide image',
  imagePullPolicy: 'IfNotPresent',
  replicas: error 'must provide replicas',
  logLevel: 'info',
  ports: {
    internal: 8081,
  },
  serviceMonitor: true,
  env: {
    observatoriumURL: '${OBSERVATORIUM_URL}',
    oidcAudience: '${OIDC_AUDIENCE}',
    oidcIssuerURL: '${OIDC_ISSUER_URL}',
    sleepDurationSeconds: '${SLEEP_DURATION_SECONDS}',
    managedTenants: '${MANAGED_TENANTS}',
    logRulesEnabled: 'true',
  },
  tenantSecretMap: error 'must provide tenantSecretMap',
  resources: {},

  commonLabels:: {
    'app.kubernetes.io/name': 'obsctl-reloader',
    'app.kubernetes.io/instance': defaults.name,
    'app.kubernetes.io/version': defaults.version,
    'app.kubernetes.io/component': 'obsctl-reloader',
  },

  podLabelSelector:: {
    [labelName]: defaults.commonLabels[labelName]
    for labelName in std.objectFields(defaults.commonLabels)
    if !std.setMember(labelName, ['app.kubernetes.io/version'])
  },
};

function(params) {
  local or = self,

  // Combine the defaults and the passed params to make the component's config.
  config:: defaults + params,
  // Safety checks for combined config of defaults and params
  assert std.isObject(or.config.resources),
  assert std.isObject(or.config.env),
  assert std.isArray(or.config.tenantSecretMap),

  serviceAccount: {
    apiVersion: 'v1',
    kind: 'ServiceAccount',
    metadata: {
      name: or.config.name,
      namespace: or.config.namespace,
      labels: or.config.commonLabels,
    },
  },

  role: {
    apiVersion: 'rbac.authorization.k8s.io/v1',
    kind: 'Role',
    metadata: {
      name: or.config.name,
      namespace: or.config.namespace,
      labels: or.config.commonLabels,
    },
    rules: [
      {
        apiGroups: ['monitoring.coreos.com'],
        resources: ['prometheusrules'],
        verbs: ['get', 'list', 'watch'],
      },
      {
        apiGroups: ['loki.grafana.com'],
        resources: ['alertingrules', 'recordingrules'],
        verbs: ['get', 'list', 'watch'],
      },
    ],
  },

  roleBinding: {
    apiVersion: 'rbac.authorization.k8s.io/v1',
    kind: 'RoleBinding',
    metadata: {
      name: or.config.name,
      namespace: or.config.namespace,
      labels: or.config.commonLabels,
    },
    roleRef: {
      apiGroup: 'rbac.authorization.k8s.io',
      kind: 'Role',
      name: or.role.metadata.name,
    },
    subjects: [
      {
        kind: 'ServiceAccount',
        name: or.serviceAccount.metadata.name,
      },
    ],
  },

  service: {
    apiVersion: 'v1',
    kind: 'Service',
    metadata: {
      name: or.config.name,
      namespace: or.config.namespace,
      labels: or.config.commonLabels,
    },
    spec: {
      selector: or.config.podLabelSelector,
      ports: [
        {
          name: name,
          port: or.config.ports[name],
          targetPort: or.config.ports[name],
        }
        for name in std.objectFields(or.config.ports)
      ],
    },
  },

  deployment: {
    apiVersion: 'apps/v1',
    kind: 'Deployment',
    metadata: {
      name: or.config.name,
      namespace: or.config.namespace,
      labels: or.config.commonLabels,
    },
    spec: {
      replicas: or.config.replicas,
      selector: { matchLabels: or.config.podLabelSelector },
      strategy: {
        rollingUpdate: {
          maxSurge: 0,
          maxUnavailable: 1,
        },
      },
      template: {
        metadata: { labels: or.config.commonLabels },
        spec: {
          serviceAccountName: or.serviceAccount.metadata.name,
          containers: [
            {
              name: 'obsctl-reloader',
              image: or.config.image,
              imagePullPolicy: or.config.imagePullPolicy,
              args: [
                '--log.level=%s' % or.config.logLevel,
                '--web.internal.listen=0.0.0.0:%d' % or.config.ports.internal,
                '--sleep-duration-seconds=%s' % or.config.env.sleepDurationSeconds,
                '--observatorium-api-url=%s' % or.config.env.observatoriumURL,
                '--managed-tenants=%s' % or.config.env.managedTenants,
                '--issuer-url=%s' % or.config.env.oidcIssuerURL,
                '--audience=%s' % or.config.env.oidcAudience,
              ] + if std.objectHas(or.config.env, 'logRulesEnabled') then ['--log-rules-enabled=%s' % or.config.env.logRulesEnabled] else [],
              resources: if or.config.resources != {} then or.config.resources else {},
              env: [
                {
                  name: 'NAMESPACE_NAME',
                  valueFrom: {
                    fieldRef: {
                      fieldPath: 'metadata.namespace',
                    },
                  },
                },
              ] + [
                {
                  name: t.tenant + '_CLIENT_ID',
                  valueFrom: {
                    secretKeyRef: {
                      name: t.secret,
                      key: t.idKey,
                      [if std.objectHas(t, 'optional') then 'optional' else null]: true,
                    },
                  },
                }
                for t in or.config.tenantSecretMap
              ] + [
                {
                  name: t.tenant + '_CLIENT_SECRET',
                  valueFrom: {
                    secretKeyRef: {
                      name: t.secret,
                      key: t.secretKey,
                      [if std.objectHas(t, 'optional') then 'optional' else null]: true,
                    },
                  },
                }
                for t in or.config.tenantSecretMap
              ],
            },
          ],
        },
      },
    },
  },

  serviceMonitor: if or.config.serviceMonitor == true then {
    apiVersion: 'monitoring.coreos.com/v1',
    kind: 'ServiceMonitor',
    metadata+: {
      name: or.config.name,
      namespace: or.config.namespace,
    },
    spec: {
      selector: {
        matchLabels: or.config.commonLabels,
      },
      endpoints: [{ port: 'internal' }],
    },
  },
}
