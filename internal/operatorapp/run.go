package operatorapp

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/controllers"
	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	cwadapter "github.com/FluxSeer/fluxseer-rca/internal/datasource/cloudwatch"
	k8sadapter "github.com/FluxSeer/fluxseer-rca/internal/datasource/kubernetes"
	lokiadapter "github.com/FluxSeer/fluxseer-rca/internal/datasource/loki"
	oteladapter "github.com/FluxSeer/fluxseer-rca/internal/datasource/opentelemetry"
	promadapter "github.com/FluxSeer/fluxseer-rca/internal/datasource/prometheus"
	"github.com/FluxSeer/fluxseer-rca/internal/datasourceconfig"
	"github.com/FluxSeer/fluxseer-rca/internal/detector"
	evidencepkg "github.com/FluxSeer/fluxseer-rca/internal/evidence"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model"
	"github.com/FluxSeer/fluxseer-rca/internal/model/claude"
	"github.com/FluxSeer/fluxseer-rca/internal/model/gemini"
	"github.com/FluxSeer/fluxseer-rca/internal/model/heuristic"
	"github.com/FluxSeer/fluxseer-rca/internal/model/openai"
	"github.com/FluxSeer/fluxseer-rca/internal/modelgateway"
	"github.com/FluxSeer/fluxseer-rca/internal/notifier/webhook"
	"github.com/FluxSeer/fluxseer-rca/internal/version"
)

func Run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("fluxseer-rca-manager", flag.ContinueOnError)
	fs.SetOutput(out)

	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var enableRemediation bool
	var enableLegacyDeploymentRisk bool
	fs.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	fs.BoolVar(&enableRemediation, "enable-remediation", false, "Enable RemediationPlan and AgentAction reconciliation.")
	fs.BoolVar(&enableLegacyDeploymentRisk, "enable-legacy-deployment-risk", false, "Enable legacy annotation-driven Deployment risk reconciliation.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	info := version.Current()
	_, _ = fmt.Fprintf(out, "fluxseer-rca starting version=%s gitCommit=%s gitDirty=%s buildDate=%s\n", info.Version, info.GitCommit, info.GitDirty, info.BuildDate)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: false})))

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
		LeaderElectionID:       "fluxseer-rca-manager.aiops.platform",
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
		executor.NotificationExecutor{WebhookURL: os.Getenv("FLUXSEER_RCA_WEBHOOK_URL")},
	)

	registry := datasource.NewRegistry(
		k8sadapter.Adapter{Client: mgr.GetClient()},
	)
	modelProviders := model.NewRegistry(
		openai.Provider{},
		gemini.Provider{},
		claude.Provider{},
		heuristic.Provider{},
	)
	gateway := &modelgateway.Gateway{
		Base:      knowledge.NewBase(),
		Providers: modelProviders,
		Secrets:   modelgateway.KubeSecretResolver{Client: mgr.GetAPIReader()},
	}
	resolver := modelgateway.KubeResolver{
		Client: mgr.GetClient(),
	}
	gateway.Resolver = resolver
	if url := os.Getenv("FLUXSEER_RCA_PROMETHEUS_URL"); url != "" {
		registry.Register(promadapter.Adapter{BaseURL: url})
	}
	if url := os.Getenv("FLUXSEER_RCA_LOKI_URL"); url != "" {
		registry.Register(lokiadapter.Adapter{BaseURL: url})
	}
	if endpoint := os.Getenv("FLUXSEER_RCA_OTEL_ENDPOINT"); endpoint != "" {
		registry.Register(oteladapter.Adapter{Endpoint: endpoint})
	}
	if region := os.Getenv("FLUXSEER_RCA_CLOUDWATCH_REGION"); region != "" {
		registry.Register(cwadapter.Adapter{Region: region})
	}
	if err := datasourceconfig.RegisterFromResources(context.Background(), mgr.GetAPIReader(), registry, mgr.GetClient()); err != nil {
		return fmt.Errorf("unable to register datasource resources: %w", err)
	}
	if err := (&controllers.DataSourceReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Registry:  registry,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create DataSource controller: %w", err)
	}

	if enableLegacyDeploymentRisk {
		detectionInterval := parseDurationEnv("FLUXSEER_RCA_SCAN_INTERVAL", 30*time.Second)
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

	if err := (&controllers.InvestigationRequestReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Service: &investigation.Service{
			Client:        mgr.GetClient(),
			Registry:      registry,
			Resolver:      resolver,
			Gateway:       gateway,
			EvidenceStore: evidenceStoreFromEnv(),
		},
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create InvestigationRequest controller: %w", err)
	}

	if webhookURL := os.Getenv("FLUXSEER_RCA_WEBHOOK_URL"); webhookURL != "" {
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

	ctrl.Log.WithName("setup").Info("starting fluxseer-rca manager")
	return mgr.Start(ctrl.SetupSignalHandler())
}

func evidenceStoreFromEnv() evidencepkg.SnapshotStore {
	root := os.Getenv("FLUXSEER_RCA_EVIDENCE_STORE_DIR")
	if strings.TrimSpace(root) == "" {
		return nil
	}
	return evidencepkg.LocalFilesystemStore{Root: root}
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
