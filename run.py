#!/usr/bin/python3

"""
Use obsctl to set rules from PrometheusRules.
"""

import json
import os
import tempfile
import subprocess

from typing import Any, Optional


def run(cmd: list[str]) -> str:
    """Run shell command."""
    result = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=True)
    return result.stdout.decode()


def get_prometheus_rules() -> dict[str, Any]:
    """Get PrometheusRules using OC cli"""
    cmd = ["oc", "get", "prometheusrules", "-o", "json"]
    result = run(cmd)
    return json.loads(result)["items"]


def get_tenant(prometheus_rule: dict[str, Any]) -> Optional[str]:
    """Get tenant label from PrometheusRule."""
    name = prometheus_rule['metadata']['name']
    try:
        tenant = prometheus_rule["metadata"]["labels"]["tenant"]
        print(f"prometheus rule {name} has tenant label {tenant}")
        return tenant
    except KeyError:
        print(f"prometheus rule {name} has no tenant label")
        return None


def get_tenant_rules(prometheus_rules: list[dict[str, Any]]
) -> dict[str, dict[str, list[dict[str, Any]]]]:
    """Get rules per tenant from PrometheusRules."""
    all_tenant_rules: dict[str, dict[str, list[dict[str, Any]]]] = {}
    for rule in prometheus_rules:
        name = rule["metadata"]["name"]

        print(f"checking prometheus rule {name} for tenant")
        tenant = get_tenant(rule)
        if not tenant:
            continue

        print(f"checking prometheus rule {name} tenant {tenant}")
        tenant_rules = all_tenant_rules.setdefault(tenant, {"groups": []})
        tenant_rules["groups"].extend(rule["spec"]["groups"])

    return all_tenant_rules


def obsctl_context_add(tenant: str) -> None:
    """Add context to obsctl."""
    cmd = [
        "obsctl",
        "context",
        "api",
        "add",
        "--name", tenant,
        "--url", os.environ["OBSERVATORIUM_URL"],
    ]
    run(cmd)


def obsctl_login(tenant: str) -> None:
    """Login using obsctl."""
    cmd = [
        "obsctl",
        "login",
        "--api", tenant,
        "--oidc.audience", os.environ["OIDC_AUDIENCE"],
        "--oidc.client-id", os.environ["OIDC_CLIENT_ID"],
        "--oidc.client-secret", os.environ["OIDC_CLIENT_SECRET"],
        "--oidc.issuer-url", os.environ["OIDC_ISSUER_URL"],
        "--tenant", tenant,
    ]
    run(cmd)


def obsctl_context_switch(tenant: str) -> None:
    """Switch context using obsctl."""
    cmd = [
        "obsctl",
        "context",
        "switch",
        f"{tenant}/{tenant}",
    ]
    run(cmd)


def obsctl_metrics_get_rules() -> Any:
    """Get rules using obsctl."""
    cmd = ["obsctl", "metrics", "get", "rules"]
    result = run(cmd)
    return json.loads(result)


def obsctl_metrics_set_rules(tenant: str, rules: dict[str, list[dict[str, Any]]]) -> None:
    """Set rules using obsctl."""
    print(f"setting metrics for tenant {tenant}")
    with tempfile.NamedTemporaryFile(mode="w+") as rule_file:
        rule_file.write(json.dumps(rules))
        rule_file.flush()
        cmd = ["obsctl", "metrics", "set", "--rule.file", rule_file.name]
        run(cmd)


def main():
    """Main execution."""
    prometheus_rules = get_prometheus_rules()
    tenant_rules = get_tenant_rules(prometheus_rules)
    for tenant, rules in tenant_rules.items():
        obsctl_context_add(tenant)
        obsctl_login(tenant)
        obsctl_context_switch(tenant)
        obsctl_metrics_set_rules(tenant, rules)


if __name__ == '__main__':
    main()
