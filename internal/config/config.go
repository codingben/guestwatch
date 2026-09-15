package config

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/labels"
)

// requiredModelSentinel is an obviously invalid placeholder. Startup
// validation rejects it explicitly so it can never ship as a working
// default; any operator who copies the example config verbatim gets a
// clear error instead of a live call to a nonexistent model.
const requiredModelSentinel = "REQUIRED_MODEL_ID"

// Defaults for the classifier rate limit and node capture concurrency, per
// the scheduling contract. These are configurable (not compiled-in
// constants) so that an operator can raise them to meet the first-pass
// deadline at their own fleet size; see ScanConfig.Validate.
const (
	DefaultClassifierRPS         = 2.0
	DefaultClassifierConcurrency = 5
	DefaultFirstPassDeadline     = 45 * time.Second

	// DefaultWorkerCount is the number of goroutines pulling targets off
	// the per-pass queue. It bounds how many screenshot captures can be
	// in flight at once, independent of ClassifierConcurrency.
	DefaultWorkerCount = 10

	// DefaultKubeAPIBurst matches client-go's own historical default, so
	// leaving it unset preserves today's behavior. Raise it together with
	// WorkerCount when scaling past a few hundred VMIs: every screenshot
	// makes two API calls (an identity check plus the VNC subresource
	// fetch), so throughput here caps out well before ClassifierRPS does
	// at fleet scale.
	DefaultKubeAPIBurst = 10
)

// DefaultKubeAPIQPS matches client-go's own historical default; see
// DefaultKubeAPIBurst. Typed as float32, matching ScanConfig.KubeAPIQPS, so
// callers never need a conversion.
const DefaultKubeAPIQPS float32 = 5.0

const (
	DefaultDashboardAddr      = ":8080"
	DefaultDashboardStaticDir = "/opt/guestwatch/ui"
)

// ScanConfig controls discovery, scheduling, and capacity.
type ScanConfig struct {
	Namespaces         []string      `yaml:"namespaces"`
	LabelSelector      string        `yaml:"labelSelector"`
	Interval           time.Duration `yaml:"interval"`
	PerNodeConcurrency int           `yaml:"perNodeConcurrency"`

	// ClassifierRPS and ClassifierConcurrency bound the model call rate
	// (see the capacity formula). They are configurable rather than
	// compiled-in so that the first-pass deadline can be met by raising
	// them, rather than only by shrinking the target set.
	ClassifierRPS         float64       `yaml:"classifierRPS"`
	ClassifierConcurrency int           `yaml:"classifierConcurrency"`
	FirstPassDeadline     time.Duration `yaml:"firstPassDeadline"`

	// WorkerCount, KubeAPIQPS, and KubeAPIBurst are the other half of the
	// capacity formula: WorkerCount bounds concurrent screenshot capture,
	// and KubeAPIQPS/KubeAPIBurst bound the rate of the underlying
	// Kubernetes/KubeVirt API calls those captures make.
	WorkerCount  int     `yaml:"workerCount"`
	KubeAPIQPS   float32 `yaml:"kubeAPIQPS"`
	KubeAPIBurst int     `yaml:"kubeAPIBurst"`
}

type MCPConfig struct {
	ConsoleURL string `yaml:"consoleURL"`
}

type ModelConfig struct {
	Classifier string `yaml:"classifier"`
}

type PrivacyConfig struct {
	ConsoleEvidenceEgressAcknowledged bool `yaml:"consoleEvidenceEgressAcknowledged"`
}

type DashboardConfig struct {
	Addr            string `yaml:"addr"`
	MaxObservations int    `yaml:"maxObservations"`

	// StaticDir is the on-disk directory the built frontend is served
	// from; not embedded into the binary.
	StaticDir string `yaml:"staticDir"`
}

type Config struct {
	Scan      ScanConfig      `yaml:"scan"`
	MCP       MCPConfig       `yaml:"mcp"`
	Model     ModelConfig     `yaml:"model"`
	Privacy   PrivacyConfig   `yaml:"privacy"`
	Dashboard DashboardConfig `yaml:"dashboard"`
}

func Load(r io.Reader) (Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Scan.ClassifierRPS == 0 {
		c.Scan.ClassifierRPS = DefaultClassifierRPS
	}
	if c.Scan.ClassifierConcurrency == 0 {
		c.Scan.ClassifierConcurrency = DefaultClassifierConcurrency
	}
	if c.Scan.FirstPassDeadline == 0 {
		c.Scan.FirstPassDeadline = DefaultFirstPassDeadline
	}
	if c.Scan.WorkerCount == 0 {
		c.Scan.WorkerCount = DefaultWorkerCount
	}
	if c.Scan.KubeAPIQPS == 0 {
		c.Scan.KubeAPIQPS = DefaultKubeAPIQPS
	}
	if c.Scan.KubeAPIBurst == 0 {
		c.Scan.KubeAPIBurst = DefaultKubeAPIBurst
	}
	if c.Dashboard.Addr == "" {
		c.Dashboard.Addr = DefaultDashboardAddr
	}
	if c.Dashboard.StaticDir == "" {
		c.Dashboard.StaticDir = DefaultDashboardStaticDir
	}
}

func (c Config) Validate() error {
	if len(c.Scan.Namespaces) == 0 {
		return fmt.Errorf("scan.namespaces must not be empty")
	}
	seen := make(map[string]struct{}, len(c.Scan.Namespaces))
	for _, ns := range c.Scan.Namespaces {
		if ns == "" {
			return fmt.Errorf("scan.namespaces must not contain an empty entry")
		}
		if _, dup := seen[ns]; dup {
			return fmt.Errorf("scan.namespaces contains duplicate %q", ns)
		}
		seen[ns] = struct{}{}
	}

	if c.Scan.LabelSelector != "" {
		if _, err := labels.Parse(c.Scan.LabelSelector); err != nil {
			return fmt.Errorf("scan.labelSelector is invalid: %w", err)
		}
	}

	if c.Scan.Interval <= 0 {
		return fmt.Errorf("scan.interval must be positive")
	}
	if c.Scan.PerNodeConcurrency < 1 {
		return fmt.Errorf("scan.perNodeConcurrency must be at least 1")
	}
	if c.Scan.ClassifierRPS <= 0 {
		return fmt.Errorf("scan.classifierRPS must be positive")
	}
	if c.Scan.ClassifierConcurrency < 1 {
		return fmt.Errorf("scan.classifierConcurrency must be at least 1")
	}
	if c.Scan.FirstPassDeadline <= 0 {
		return fmt.Errorf("scan.firstPassDeadline must be positive")
	}
	if c.Scan.WorkerCount < 1 {
		return fmt.Errorf("scan.workerCount must be at least 1")
	}
	if c.Scan.KubeAPIQPS <= 0 {
		return fmt.Errorf("scan.kubeAPIQPS must be positive")
	}
	if c.Scan.KubeAPIBurst < 1 {
		return fmt.Errorf("scan.kubeAPIBurst must be at least 1")
	}

	if c.MCP.ConsoleURL == "" {
		return fmt.Errorf("mcp.consoleURL must not be empty")
	}
	u, err := url.Parse(c.MCP.ConsoleURL)
	if err != nil || u.Host == "" || !(u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname()))) {
		return fmt.Errorf("mcp.consoleURL must be a valid https URL (plain http is only allowed to a loopback host, e.g. the console-mcp sidecar)")
	}

	if c.Model.Classifier == "" {
		return fmt.Errorf("model.classifier must not be empty")
	}
	if strings.EqualFold(c.Model.Classifier, requiredModelSentinel) || strings.HasPrefix(strings.ToUpper(c.Model.Classifier), "REQUIRED_") {
		return fmt.Errorf("model.classifier must be set to a real model ID, not a REQUIRED_ placeholder")
	}

	if !c.Privacy.ConsoleEvidenceEgressAcknowledged {
		return fmt.Errorf("privacy.consoleEvidenceEgressAcknowledged must be true: screenshots leave the cluster for the configured model provider")
	}

	if c.Dashboard.Addr != "" {
		if _, _, err := net.SplitHostPort(c.Dashboard.Addr); err != nil {
			return fmt.Errorf("dashboard.addr is invalid: %w", err)
		}
	}
	if c.Dashboard.MaxObservations < 0 {
		return fmt.Errorf("dashboard.maxObservations must not be negative")
	}

	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
