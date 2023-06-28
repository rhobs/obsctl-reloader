package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"syscall"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	lokiv1 "github.com/grafana/loki/operator/apis/loki/v1"
	lokiv1beta1 "github.com/grafana/loki/operator/apis/loki/v1beta1"
	"github.com/metalmatze/signal/internalserver"
	"github.com/oklog/run"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/rhobs/obsctl-reloader/pkg/loader"
	"github.com/rhobs/obsctl-reloader/pkg/loop"
	"github.com/rhobs/obsctl-reloader/pkg/syncer"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	k8sconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	obsctlContextAPIName        = "api"
	defaultSleepDurationSeconds = 15
)

type cfg struct {
	observatoriumURL     string
	sleepDurationSeconds uint
	managedTenants       string
	audience             string
	issuerURL            string
	logRulesEnabled      bool
	logLevel             string
	listenInternal       string
}

func setupLogger(logLevel string) log.Logger {
	var lvl level.Option
	switch logLevel {
	case "error":
		lvl = level.AllowError()
	case "warn":
		lvl = level.AllowWarn()
	case "info":
		lvl = level.AllowInfo()
	case "debug":
		lvl = level.AllowDebug()
	default:
		panic("unexpected log level")
	}

	logger := level.NewFilter(log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)), lvl)

	logger = log.With(logger, "name", "obsctl-reloader")
	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
	return logger
}

func parseFlags() *cfg {
	cfg := &cfg{}

	// Common flags.
	flag.UintVar(&cfg.sleepDurationSeconds, "sleep-duration-seconds", defaultSleepDurationSeconds, "The interval in seconds after which all PrometheusRules are synced to Observatorium API.")
	flag.StringVar(&cfg.observatoriumURL, "observatorium-api-url", "", "The URL of the Observatorium API to which rules will be synced.")
	flag.StringVar(&cfg.managedTenants, "managed-tenants", "", "The name of the tenants whose rules should be synced. If there are multiple tenants, ensure they are comma-separated.")
	flag.StringVar(&cfg.issuerURL, "issuer-url", "", "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	flag.StringVar(&cfg.audience, "audience", "", "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")
	flag.BoolVar(&cfg.logRulesEnabled, "log-rules-enabled", true, "Enable syncing Loki logging rules. Always on by default.")

	flag.StringVar(&cfg.logLevel, "log.level", "info", "Log filtering level. One of: debug, info, warn, error.")
	flag.StringVar(&cfg.listenInternal, "web.internal.listen", ":8081", "The address on which the internal server listens.")

	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()

	ctx, cancel := context.WithCancel(context.Background())

	namespace := os.Getenv("NAMESPACE_NAME")
	if namespace == "" {
		panic("Missing env var NAMESPACE_NAME")
	}

	logger := setupLogger(cfg.logLevel)
	defer level.Info(logger).Log("msg", "exiting")

	// Create kubernetes client for deployments
	k8sCfg, err := k8sconfig.GetConfig()
	if err != nil {
		panic("Failed to read kubeconfig")
	}

	mapper, err := apiutil.NewDynamicRESTMapper(k8sCfg)
	if err != nil {
		panic("Failed to create new dynamic REST mapper")
	}

	err = monitoringv1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic("Failed to register monitoringv1 types to runtime scheme")
	}

	if cfg.logRulesEnabled {
		err = lokiv1beta1.AddToScheme(scheme.Scheme)
		if err != nil {
			panic("Failed to register lokiv1beta1 types to runtime scheme")
		}

		err = lokiv1.AddToScheme(scheme.Scheme)
		if err != nil {
			panic("Failed to register lokiv1 types to runtime scheme")
		}
	}

	opts := client.Options{Scheme: scheme.Scheme, Mapper: mapper}
	k8sClient, err := client.New(k8sCfg, opts)
	if err != nil {
		panic("Failed to create new k8s client")
	}

	// Create prometheus registry.
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		//nolint:exhaustivestruct
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// Initialize config.
	o := syncer.NewObsctlRulesSyncer(
		ctx,
		log.With(logger, "component", "obsctl-syncer"),
		k8sClient,
		namespace,
		cfg.observatoriumURL,
		cfg.audience,
		cfg.issuerURL,
		cfg.managedTenants,
		reg,
	)
	if err := o.InitOrReloadObsctlConfig(); err != nil {
		level.Error(logger).Log("msg", "error reloading/initializing obsctl config", "error", err)
		panic(err)
	}

	var g run.Group
	{
		g.Add(run.SignalHandler(ctx, os.Interrupt, syscall.SIGINT, syscall.SIGTERM))
	}
	{
		g.Add(func() error {
			level.Info(logger).Log("msg", "starting obsctl-reloader sync")
			return loop.SyncLoop(ctx, logger,
				loader.NewKubeRulesLoader(ctx, k8sClient, logger, namespace, cfg.managedTenants, reg),
				o,
				cfg.logRulesEnabled,
				cfg.sleepDurationSeconds,
			)
		}, func(_ error) {
			cancel()
		})
	}
	{
		h := internalserver.NewHandler(
			internalserver.WithName("Internal - obsctl-reloader"),
			internalserver.WithPrometheusRegistry(reg),
			internalserver.WithPProf(),
		)

		//nolint:exhaustivestruct
		s := http.Server{
			Addr:    cfg.listenInternal,
			Handler: h,
		}

		g.Add(func() error {
			level.Info(logger).Log("msg", "starting internal HTTP server", "address", s.Addr)

			return s.ListenAndServe() //nolint:wrapcheck
		}, func(_ error) {
			_ = s.Shutdown(ctx)
			cancel()
		})
	}

	if err := g.Run(); err != nil {
		level.Error(logger).Log("msg", "starting run group", "error", err)
		os.Exit(1)
	}
}
