package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/codingben/kubevirt-ai-agent/internal/agent"
	"github.com/codingben/kubevirt-ai-agent/internal/config"
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
	configPath := flag.String("config", "/etc/kubevirt-ai-agent/config.yaml", "path to the scan configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	virtClient, err := getVirtClient()
	if err != nil {
		return fmt.Errorf("build kubevirt client: %w", err)
	}

	openaiClient := openai.NewClient(option.WithMaxRetries(0))
	classifier, err := model.NewOpenAI(model.OpenAIConfig{
		ClassifierModel:    cfg.Model.Classifier,
		RequestsPerSecond:  cfg.Scan.ClassifierRPS,
		ConcurrentRequests: cfg.Scan.ClassifierConcurrency,
	}, openaiClient)
	if err != nil {
		return fmt.Errorf("build classifier: %w", err)
	}

	console, err := mcp.NewClient(ctx, mcp.ClientConfig{
		ConsoleURL:     cfg.MCP.ConsoleURL,
		MaxImageBytes:  mcp.DefaultMaxImageBytes,
		MaxImagePixels: mcp.DefaultMaxImagePixels,
	}, identityReader{client: virtClient})
	if err != nil {
		return fmt.Errorf("build console MCP client: %w", err)
	}

	scanner, err := agent.NewScanner(cfg, agent.ScannerOptions{
		VMIClient:  virtClient,
		Console:    console,
		Classifier: classifier,
		Logger:     logger,
	})
	if err != nil {
		return fmt.Errorf("build scanner: %w", err)
	}

	logger.Info("kubevirt-ai-agent starting",
		"namespaces", cfg.Scan.Namespaces,
		"interval", cfg.Scan.Interval.String(),
	)

	if err := scanner.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("scanner: %w", err)
	}
	return nil
}

func loadConfig(path string) (config.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return config.Config{}, err
	}
	defer f.Close()
	return config.Load(f)
}

func getVirtClient() (kubecli.KubevirtClient, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		kubeconfigPath := os.Getenv("KUBECONFIG")
		if kubeconfigPath == "" {
			kubeconfigPath = clientcmd.RecommendedHomeFile
		}
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("no in-cluster config and no usable kubeconfig: %w", err)
		}
	}
	return kubecli.GetKubevirtClientFromRESTConfig(restConfig)
}

type identityReader struct {
	client kubecli.KubevirtClient
}

func (r identityReader) GetVMI(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	return r.client.VirtualMachineInstance(namespace).Get(ctx, name, metav1.GetOptions{})
}
