package loop

import (
	"context"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"github.com/rhobs/obsctl-reloader/pkg/loader"
	"github.com/rhobs/obsctl-reloader/pkg/syncer"
)

// SyncLoop represents the main loop of this controller, which syncs PrometheusRule and Loki's AlertingRule/RecordingRule
// objects of each managed tenant with Observatorium API every n seconds.
func SyncLoop(
	ctx context.Context,
	logger log.Logger,
	k loader.RulesLoader,
	o syncer.RulesSyncer,
	logRulesEnabled bool,
	sleepDurationSeconds uint,
	configReloadIntervalSeconds uint,
) error {
	for {
		select {
		case <-time.After(time.Duration(configReloadIntervalSeconds) * time.Second):
			if err := o.InitOrReloadObsctlConfig(); err != nil {
				level.Error(logger).Log("msg", "error reloading obsctl config", "error", err)
			}
		case <-time.After(time.Duration(sleepDurationSeconds) * time.Second):
			prometheusRules, err := k.GetPrometheusRules()
			if err != nil {
				level.Error(logger).Log("msg", "error getting prometheus rules", "error", err, "rules", len(prometheusRules))
				return err
			}

			// Set each tenant as current and set rules.
			for tenant, ruleGroups := range k.GetTenantMetricsRuleGroups(prometheusRules) {
				err = o.MetricsSet(tenant, ruleGroups)
				if err != nil {
					level.Error(logger).Log("msg", "error setting rules", "tenant", tenant, "error", err)
					continue
				}
			}

			if logRulesEnabled {
				lokiAlertingRules, err := k.GetLokiAlertingRules()
				if err != nil {
					level.Error(logger).Log("msg", "error getting loki alerting rules", "error", err, "rules", len(lokiAlertingRules))
					return err
				}

				for tenant, ruleGroups := range k.GetTenantLogsAlertingRuleGroups(lokiAlertingRules) {
					if err := o.SetCurrentTenant(tenant); err != nil {
						level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
						continue
					}

					err = o.LogsAlertingSet(ruleGroups)
					if err != nil {
						level.Error(logger).Log("msg", "error setting loki alerting rules", "tenant", tenant, "error", err)
						continue
					}
				}

				lokiRecordingRules, err := k.GetLokiRecordingRules()
				if err != nil {
					level.Error(logger).Log("msg", "error getting loki recording rules", "error", err, "rules", len(lokiRecordingRules))
					return err
				}

				for tenant, ruleGroups := range k.GetTenantLogsRecordingRuleGroups(lokiRecordingRules) {
					if err := o.SetCurrentTenant(tenant); err != nil {
						level.Error(logger).Log("msg", "error setting tenant", "tenant", tenant, "error", err)
						continue
					}

					err = o.LogsRecordingSet(ruleGroups)
					if err != nil {
						level.Error(logger).Log("msg", "error setting loki recording rules", "tenant", tenant, "error", err)
						continue
					}
				}
			}

			level.Debug(logger).Log("msg", "sleeping", "duration", sleepDurationSeconds)
		case <-ctx.Done():
			return nil
		}
	}
}
