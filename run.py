#!/usr/bin/python3

"""
Use obsctl to set rules from PrometheusRules.
"""

import json
import logging
import os
import tempfile
import subprocess
import sys
import time

from typing import Any, Optional


OBSCTL_CONTEXT = "api"
DEFAULT_SLEEP_DURATION_SECONDS = 30


def setup_logging() -> None:
    """Setup logging format and handler."""
    log_format = (
        "[%(asctime)s] [%(levelname)s] "
        "[%(filename)s:%(funcName)s:%(lineno)d] - %(message)s"
    )
    date_format = "%Y-%m-%d %H:%M:%S"
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(logging.Formatter(fmt=log_format, datefmt=date_format))
    logging.basicConfig(level=logging.INFO, handlers=[handler])


def run(cmd: list[str]) -> str:
    """Run shell command."""
    result = subprocess.run(
        cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=True
    )
    return result.stdout.decode()


def get_prometheus_rules() -> dict[str, Any]:
    """Get PrometheusRules using OC cli"""
    cmd = ["oc", "get", "prometheusrules", "-o", "json"]
    result = run(cmd)
    return json.loads(result)["items"]


def get_tenant(prometheus_rule: dict[str, Any]) -> Optional[str]:
    """Get tenant label from PrometheusRule."""
    name = prometheus_rule["metadata"]["name"]
    try:
        tenant = prometheus_rule["metadata"]["labels"]["tenant"]
        logging.info("prometheus rule %s has tenant label %s", name, tenant)
        return tenant
    except KeyError:
        logging.info("prometheus rule %s has no tenant label", name)
        return None


def get_tenant_rules(
    prometheus_rules: list[dict[str, Any]]
) -> dict[str, dict[str, list[dict[str, Any]]]]:
    """Get rules per tenant from PrometheusRules."""
    all_tenant_rules: dict[str, dict[str, list[dict[str, Any]]]] = {}
    for rule in prometheus_rules:
        name = rule["metadata"]["name"]

        logging.info("checking prometheus rule %s for tenant", name)
        tenant = get_tenant(rule)
        if not tenant:
            continue

        logging.info("checking prometheus rule %s tenant %s", name, tenant)
        tenant_rules = all_tenant_rules.setdefault(tenant, {"groups": []})
        tenant_rules["groups"].extend(rule["spec"]["groups"])

    return all_tenant_rules


def obsctl_context_add() -> None:
    """Add context to obsctl."""
    cmd = [
        "obsctl",
        "context",
        "api",
        "add",
        "--name",
        OBSCTL_CONTEXT,
        "--url",
        os.environ["OBSERVATORIUM_URL"],
    ]
    run(cmd)


def obsctl_context_remove() -> None:
    """Remove context using obsctl."""
    cmd = [
        "obsctl",
        "context",
        "api",
        "rm",
        "--name",
        OBSCTL_CONTEXT,
    ]
    run(cmd)


def obsctl_login(tenant: str) -> None:
    """Login using obsctl."""
    cmd = [
        "obsctl",
        "login",
        "--api",
        OBSCTL_CONTEXT,
        "--oidc.audience",
        os.environ["OIDC_AUDIENCE"],
        "--oidc.client-id",
        os.environ["OIDC_CLIENT_ID"],
        "--oidc.client-secret",
        os.environ["OIDC_CLIENT_SECRET"],
        "--oidc.issuer-url",
        os.environ["OIDC_ISSUER_URL"],
        "--tenant",
        tenant,
    ]
    run(cmd)


def obsctl_context_switch(tenant: str) -> None:
    """Switch context using obsctl."""
    cmd = [
        "obsctl",
        "context",
        "switch",
        f"{OBSCTL_CONTEXT}/{tenant}",
    ]
    run(cmd)


def obsctl_metrics_get_rules() -> Any:
    """Get rules using obsctl."""
    cmd = ["obsctl", "metrics", "get", "rules"]
    result = run(cmd)
    return json.loads(result)


def obsctl_metrics_set_rules(
    tenant: str, rules: dict[str, list[dict[str, Any]]]
) -> None:
    """Set rules using obsctl."""
    logging.info("setting metrics for tenant %s", tenant)
    with tempfile.NamedTemporaryFile(mode="w+") as rule_file:
        rule_file.write(json.dumps(rules))
        rule_file.flush()
        cmd = ["obsctl", "metrics", "set", "--rule.file", rule_file.name]
        run(cmd)


def sleep():
    """Sleep between iterations."""
    sleep_duration_seconds = int(
        os.environ.get("SLEEP_DURATION_SECONDS", DEFAULT_SLEEP_DURATION_SECONDS)
    )
    logging.info("sleeping for %d seconds", sleep_duration_seconds)
    time.sleep(sleep_duration_seconds)


def main():
    """Main execution."""
    setup_logging()
    while True:
        prometheus_rules = get_prometheus_rules()
        tenant_rules = get_tenant_rules(prometheus_rules)
        for tenant, rules in tenant_rules.items():
            try:
                obsctl_context_add()
                obsctl_login(tenant)
                obsctl_context_switch(tenant)
                obsctl_metrics_set_rules(tenant, rules)
            finally:
                obsctl_context_remove()
        sleep()


if __name__ == "__main__":
    main()
