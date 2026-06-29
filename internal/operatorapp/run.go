package operatorapp

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/controllers"
	"fluxagent/internal/datasource"
	cwadapter "fluxagent/internal/datasource/cloudwatch"
	k8sadapter "fluxagent/internal/datasource/kubernetes"
	lokiadapter "fluxagent/internal/datasource/loki"
	oteladapter "fluxagent/internal/datasource/opentelemetry"
	promadapter "fluxagent/internal/datasource/prometheus"
	"fluxagent/internal/datasourceconfig"
	"fluxagent/internal/detector"
	"fluxagent/internal/executor"
	"fluxagent/internal/guardrails"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/model/claude"
	"fluxagent/internal/model/gemini"
	"fluxagent/internal/model/heuristic"
	"fluxagent/internal/model/local"
	"fluxagent/internal/model/openai"
	"fluxagent/internal/modelgateway"
	"fluxagent/internal/notifier/webhook"
)

func Run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("fluxagent-manager", flag.ContinueOnError)
	fs.SetOutput(out)

	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var enableRemediation bool
	fs.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	fs.BoolVar(&enableRemediation, "enable-remediation", false, "Enable RemediationPlan and AgentAction reconciliation.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "fluxagent-manager.aiops.platform",
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	policyEngine := guardrails.NewEngine(guardrails.Policy{
		AllowedActionTypes: []string{
			"kubernetes.scaleDeployment",
			"kubernetes.rolloutPause",
			"gitops.createPullRequest",
			"runbook.triggerWorkflow",
			"notification.sendSlack",
		},
		ProtectedNamespaces:      []string{"prod", "kube-system"},
		AutoApproveMaxSeverity:   "low",
		RequireApprovalAtOrAbove: "medium",
	})

	executorRouter := executor.NewRouter(
		executor.KubernetesExecutor{},
		executor.GitOpsExecutor{},
		executor.RunbookExecutor{},
		executor.NotificationExecutor{WebhookURL: os.Getenv("FLUXAGENT_WEBHOOK_URL")},
	)

	registry := datasource.NewRegistry(
		k8sadapter.Adapter{Client: mgr.GetClient()},
	)
	modelProviders := model.NewRegistry(
		openai.Provider{},
		gemini.Provider{},
		claude.Provider{},
		heuristic.Provider{},
		local.Provider{},
	)
	gateway := &modelgateway.Gateway{
		Base:      knowledge.NewBase(),
		Providers: modelProviders,
		Secrets:   modelgateway.KubeSecretResolver{Client: mgr.GetAPIReader()},
	}
	resolver := modelgateway.KubeResolver{
		Client: mgr.GetClient(),
	}
	if url := os.Getenv("FLUXAGENT_PROMETHEUS_URL"); url != "" {
		registry.Register(promadapter.Adapter{BaseURL: url})
	}
	if url := os.Getenv("FLUXAGENT_LOKI_URL"); url != "" {
		registry.Register(lokiadapter.Adapter{BaseURL: url})
	}
	if endpoint := os.Getenv("FLUXAGENT_OTEL_ENDPOINT"); endpoint != "" {
		registry.Register(oteladapter.Adapter{Endpoint: endpoint})
	}
	if region := os.Getenv("FLUXAGENT_CLOUDWATCH_REGION"); region != "" {
		registry.Register(cwadapter.Adapter{Region: region})
	}
	if err := datasourceconfig.RegisterFromResources(context.Background(), mgr.GetAPIReader(), registry, mgr.GetClient()); err != nil {
		return fmt.Errorf("unable to register datasource resources: %w", err)
	}
	detectionInterval := parseDurationEnv("FLUXAGENT_SCAN_INTERVAL", 30*time.Second)

	if err := (&controllers.DataSourceReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create DataSource controller: %w", err)
	}

	if err := (&controllers.DeploymentRiskReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Detector: &detector.Service{
			Registry: registry,
		},
		Interval: detectionInterval,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create DeploymentRisk controller: %w", err)
	}

	if err := (&controllers.RiskRuleReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Registry: registry,
		Resolver: resolver,
		Gateway:  gateway,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create RiskRule controller: %w", err)
	}

	if webhookURL := os.Getenv("FLUXAGENT_WEBHOOK_URL"); webhookURL != "" {
		if err := (&controllers.RiskSignalNotificationReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Notifier: webhook.Notifier{URL: webhookURL},
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create RiskSignal notification controller: %w", err)
		}
	}

	if enableRemediation {
		if err := (&controllers.RemediationPlanReconciler{
			Client:     mgr.GetClient(),
			Scheme:     mgr.GetScheme(),
			Guardrails: policyEngine,
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create RemediationPlan controller: %w", err)
		}
		if err := (&controllers.AgentActionReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Executor: executorRouter,
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create AgentAction controller: %w", err)
		}
	}

	if err := (&controllers.RiskSignalReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Enabled: enableRemediation,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create RiskSignal controller: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up ready check: %w", err)
	}

	ctrl.Log.WithName("setup").Info("starting fluxagent manager")
	return mgr.Start(ctrl.SetupSignalHandler())
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		return duration
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}
