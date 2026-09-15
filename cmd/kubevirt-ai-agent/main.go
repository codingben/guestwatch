package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"golang.org/x/sync/errgroup"

	"github.com/codingben/kubevirt-ai-agent/internal/agent"
	"github.com/codingben/kubevirt-ai-agent/internal/config"
	"github.com/codingben/kubevirt-ai-agent/internal/dashboard"
	"github.com/codingben/kubevirt-ai-agent/internal/mcp"
	"github.com/codingben/kubevirt-ai-agent/internal/model"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	const defaultConfigPath = "/etc/kubevirt-ai-agent/config.yaml"

	configPath := flag.String("config", defaultConfigPath, "path to the scan configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runCtx, cancelRun := context.WithCancel(sigCtx)
	defer cancelRun()

	scanner, dashboardServer, err := buildServices(runCtx, cfg, logger)
	if err != nil {
		return err
	}

	logger.Info("kubevirt-ai-agent starting",
		"namespaces", cfg.Scan.Namespaces,
		"interval", cfg.Scan.Interval.String(),
		"dashboard_addr", cfg.Dashboard.Addr,
		"dashboard_max_observations", cfg.Dashboard.MaxObservations,
		"dashboard_static_dir", cfg.Dashboard.StaticDir,
	)

	var g errgroup.Group

	g.Go(func() error {
		defer cancelRun()
		if err := scanner.Run(runCtx); !isShutdownErr(err) {
			return fmt.Errorf("scanner: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		defer cancelRun()
		if err := dashboardServer.Run(runCtx); !isShutdownErr(err) {
			return fmt.Errorf("dashboard: %w", err)
		}
		return nil
	})

	return g.Wait()
}

func buildServices(ctx context.Context, cfg config.Config, logger *slog.Logger) (*agent.Scanner, *dashboard.Server, error) {
	virtClient, err := getVirtClient(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build kubevirt client: %w", err)
	}

	openaiClient := openai.NewClient(option.WithMaxRetries(0))
	classifier, err := model.NewOpenAI(model.OpenAIConfig{
		ClassifierModel:    cfg.Model.Classifier,
		RequestsPerSecond:  cfg.Scan.ClassifierRPS,
		ConcurrentRequests: cfg.Scan.ClassifierConcurrency,
	}, openaiClient)
	if err != nil {
		return nil, nil, fmt.Errorf("build classifier: %w", err)
	}

	console, err := mcp.NewClient(ctx, mcp.ClientConfig{
		ConsoleURL:     cfg.MCP.ConsoleURL,
		MaxImageBytes:  mcp.DefaultMaxImageBytes,
		MaxImagePixels: mcp.DefaultMaxImagePixels,
		WakeScreen:     *cfg.MCP.WakeScreen,
	}, identityReader{client: virtClient})
	if err != nil {
		return nil, nil, fmt.Errorf("build console MCP client: %w", err)
	}

	store := dashboard.NewStore(cfg.Dashboard.MaxObservations)
	scanner, err := agent.NewScanner(cfg, agent.ScannerOptions{
		VMIClient:  virtClient,
		Console:    console,
		Classifier: classifier,
		Logger:     logger,
		Recorder:   store,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build scanner: %w", err)
	}

	return scanner, dashboard.NewServer(cfg.Dashboard.Addr, cfg.Dashboard.StaticDir, store, logger), nil
}

func isShutdownErr(err error) bool {
	return err == nil || errors.Is(err, context.Canceled)
}

func loadConfig(path string) (config.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return config.Config{}, err
	}
	defer f.Close()
	return config.Load(f)
}

func getVirtClient(cfg config.Config) (kubecli.KubevirtClient, error) {
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("no usable kubeconfig and no in-cluster config: %w", err)
	}

	restConfig.QPS = cfg.Scan.KubeAPIQPS
	restConfig.Burst = cfg.Scan.KubeAPIBurst

	return kubecli.GetKubevirtClientFromRESTConfig(restConfig)
}

type identityReader struct {
	client kubecli.KubevirtClient
}

func (r identityReader) GetVMI(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	return r.client.VirtualMachineInstance(namespace).Get(ctx, name, metav1.GetOptions{})
}
