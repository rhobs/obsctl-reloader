local or = (import '../jsonnet/lib/obsctl-reloader.libsonnet')({
  name: 'obsctl-reloader',
  namespace: 'observatorium-stage',
  version: 'latest',
  image: '${IMAGE}:${IMAGE_TAG}',
  replicas: '${REPLICAS}',
  env: {
    observatoriumURL: '${OBSERVATORIUM_URL}',
    oidcAudience: '${OIDC_AUDIENCE}',
    oidcIssuerURL: '${OIDC_ISSUER_URL}',
    sleepDurationSeconds: '${SLEEP_DURATION_SECONDS}',
    managedTenants: '${MANAGED_TENANTS}',
  },
  tenantSecretMap: [
    {
      tenant: 'RHOBS',
      secret: '${RHOBS_SECRET_NAME}',
      idKey: 'client_id',
      secretKey: 'client_secret',
    },
    {
      tenant: 'OSD',
      secret: '${OSD_SECRET_NAME}',
      idKey: 'client-id',
      secretKey: 'client-secret',
      optional: true
    },

  ],
});
{
  apiVersion: 'template.openshift.io/v1',
  kind: 'Template',
  metadata: { name: 'obsctl-reloader' },
  objects:
    [
      or[name]
      for name in std.objectFields(or)
    ],
  parameters: [
    { name: 'IMAGE', value: 'quay.io/app-sre/obsctl-reloader' },
    { name: 'IMAGE_TAG', value: 'latest' },
    { name: 'OBSERVATORIUM_URL', value: 'https://observatorium.api.stage.openshift.com' },
    { name: 'OIDC_AUDIENCE', value: 'observatorium' },
    { name: 'OIDC_ISSUER_URL', value: 'https://sso.redhat.com/auth/realms/redhat-external' },
    { name: 'RHOBS_SECRET_NAME', value: 'rhobs-tenant-staging' },
    { name: 'SLEEP_DURATION_SECONDS', value: 30 },
    { name: 'MANAGED_TENANTS', value: 'rhobs' },
    { name: 'REPLICAS', value: '1' },
  ],
}
