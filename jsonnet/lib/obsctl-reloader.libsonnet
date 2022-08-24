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
  env: error 'must provide env',
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
                {
                  name: 'OBSERVATORIUM_URL',
                  value: or.config.env.observatoriumURL,
                },
                {
                  name: 'OIDC_AUDIENCE',
                  value: or.config.env.oidcAudience,
                },
                {
                  name: 'OIDC_ISSUER_URL',
                  value: or.config.env.oidcIssuerURL,
                },
                {
                  name: 'SLEEP_DURATION_SECONDS',
                  value: or.config.env.sleepDurationSeconds,
                },
                {
                  name: 'MANAGED_TENANTS',
                  value: or.config.env.managedTenants,
                },
                {
                  name: 'OIDC_CLIENT_ID',
                  valueFrom: {
                    secretKeyRef: {
                      name: or.config.env.obsctlReloaderSecret,
                      key: 'client_id',
                    },
                  },
                },
                {
                  name: 'OIDC_CLIENT_SECRET',
                  valueFrom: {
                    secretKeyRef: {
                      name: or.config.env.obsctlReloaderSecret,
                      key: 'client_secret',
                    },
                  },
                },
              ],
            },
          ],
        },
      },
    },
  },
}
