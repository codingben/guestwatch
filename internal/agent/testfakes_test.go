package agent_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"github.com/codingben/kubevirt-ai-agent/internal/config"
	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

type fakeVMIInterface struct {
	kubecli.VirtualMachineInstanceInterface
	listFunc func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error)
	getFunc  func(ctx context.Context, name string, opts metav1.GetOptions) (*kubevirtv1.VirtualMachineInstance, error)
}

func (f *fakeVMIInterface) List(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
	return f.listFunc(ctx, opts)
}

func (f *fakeVMIInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*kubevirtv1.VirtualMachineInstance, error) {
	if f.getFunc != nil {
		return f.getFunc(ctx, name, opts)
	}
	return nil, errors.New("get not configured")
}

type fakeVMIClient struct {
	byNamespace map[string]*fakeVMIInterface
}

func (f *fakeVMIClient) VirtualMachineInstance(namespace string) kubecli.VirtualMachineInstanceInterface {
	return f.byNamespace[namespace]
}

func eligibleVMI(namespace, name, node string, uid types.UID) kubevirtv1.VirtualMachineInstance {
	return kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: uid},
		Status: kubevirtv1.VirtualMachineInstanceStatus{
			Phase:    kubevirtv1.Running,
			NodeName: node,
		},
	}
}

type fakeConsole struct {
	mu       sync.Mutex
	delay    time.Duration
	err      error
	captures int
}

func (f *fakeConsole) Screenshot(ctx context.Context, target domain.Target) (domain.Screenshot, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return domain.Screenshot{}, ctx.Err()
		}
	}
	f.mu.Lock()
	f.captures++
	f.mu.Unlock()
	if f.err != nil {
		return domain.Screenshot{}, f.err
	}
	return domain.Screenshot{PNG: []byte("png"), CapturedAt: time.Now()}, nil
}

func (f *fakeConsole) captureCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.captures
}

type fakeClassifier struct {
	mu        sync.Mutex
	result    domain.ClassificationResult
	err       error
	byUID     map[types.UID]domain.ClassificationResult
	callCount int
}

func (f *fakeClassifier) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

func (f *fakeClassifier) Classify(ctx context.Context, req domain.ClassifyRequest) (domain.ClassificationResult, domain.Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.err != nil {
		return domain.ClassificationResult{}, domain.Usage{}, f.err
	}
	if r, ok := f.byUID[req.Target.UID]; ok {
		return r, domain.Usage{InputTokens: 1}, nil
	}
	return f.result, domain.Usage{InputTokens: 1}, nil
}

type fakeRecorder struct {
	mu           sync.Mutex
	observations []domain.Observation
	scans        []domain.ScanRecord
}

func (f *fakeRecorder) RecordObservation(obs domain.Observation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, obs)
}

func (f *fakeRecorder) RecordScan(rec domain.ScanRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans = append(f.scans, rec)
}

func (f *fakeRecorder) Prune([]string, map[string]struct{}) {}

func (f *fakeRecorder) snapshot() ([]domain.Observation, []domain.ScanRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	obs := make([]domain.Observation, len(f.observations))
	copy(obs, f.observations)
	scans := make([]domain.ScanRecord, len(f.scans))
	copy(scans, f.scans)
	return obs, scans
}

type logEntry struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

type captureHandler struct {
	mu      *sync.Mutex
	entries *[]logEntry
}

func newCaptureLogger() (*slog.Logger, func() []logEntry) {
	var mu sync.Mutex
	var entries []logEntry
	h := &captureHandler{mu: &mu, entries: &entries}
	get := func() []logEntry {
		mu.Lock()
		defer mu.Unlock()
		out := make([]logEntry, len(entries))
		copy(out, entries)
		return out
	}
	return slog.New(h), get
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.entries = append(*h.entries, logEntry{level: r.Level, msg: r.Message, attrs: attrs})
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	default:
		panic("asInt: not an integer")
	}
}

func findLog(entries []logEntry, msg string) []logEntry {
	var out []logEntry
	for _, e := range entries {
		if e.msg == msg {
			out = append(out, e)
		}
	}
	return out
}

func singleNamespaceClient(vmis ...kubevirtv1.VirtualMachineInstance) *fakeVMIClient {
	return &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
		"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
			return &kubevirtv1.VirtualMachineInstanceList{Items: vmis}, nil
		}},
	}}
}

func baseScanConfig() config.Config {
	cfg := config.Config{
		Scan: config.ScanConfig{
			Namespaces:            []string{"ns-a"},
			Interval:              time.Hour,
			PerNodeConcurrency:    1,
			ClassifierRPS:         1000,
			ClassifierConcurrency: 100,
			FirstPassDeadline:     2 * time.Second,
			WorkerCount:           10,
			KubeAPIQPS:            50,
			KubeAPIBurst:          100,
		},
		MCP:     config.MCPConfig{ConsoleURL: "https://console-mcp:8443/mcp"},
		Model:   config.ModelConfig{Classifier: "test-model"},
		Privacy: config.PrivacyConfig{ConsoleEvidenceEgressAcknowledged: true},
	}
	return cfg
}
