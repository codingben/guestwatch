package agent_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/codingben/guestwatch/internal/agent"
	"github.com/codingben/guestwatch/internal/dashboard"
	"github.com/codingben/guestwatch/internal/domain"
)

var _ = Describe("Scanner", func() {
	It("captures, classifies, and reports a no-target-failure scan", func() {
		vmi := eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1")
		client := singleNamespaceClient(vmi)
		console := &fakeConsole{}
		classifier := &fakeClassifier{result: domain.ClassificationResult{
			Classification: domain.NoTargetFailureVisible,
			ReasonCode:     domain.NoFailureVisible,
		}}
		logger, entries := newCaptureLogger()

		scanner, err := agent.NewScanner(baseScanConfig(), agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() []logEntry { return findLog(entries(), "scan_complete") }, "5s").Should(HaveLen(1))
		sc := findLog(entries(), "scan_complete")[0]
		Expect(sc.attrs["selected"]).To(BeNumerically("==", 1))
		Expect(sc.attrs["captured"]).To(BeNumerically("==", 1))
		Expect(sc.attrs["classified"]).To(BeNumerically("==", 1))
		Expect(sc.attrs["no_target_failure"]).To(BeNumerically("==", 1))
		Expect(sc.attrs["suspected"]).To(BeNumerically("==", 0))
	})

	It("re-reports a persistent suspected finding on every scan", func() {
		vmi := eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1")
		client := singleNamespaceClient(vmi)
		console := &fakeConsole{}
		classifier := &fakeClassifier{result: domain.ClassificationResult{
			Classification: domain.SuspectedKernelPanic,
			ReasonCode:     domain.KernelPanicVisible,
		}}
		logger, entries := newCaptureLogger()
		cfg := baseScanConfig()
		cfg.Scan.Interval = 20 * time.Millisecond

		scanner, err := agent.NewScanner(cfg, agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() int { return len(findLog(entries(), "scan_complete")) }, "5s").Should(BeNumerically(">=", 3))
		cancel()

		// There is no duplicate suppression: a VMI left panicking fires one
		// finding per scan. Take a single snapshot — a scan still in flight
		// would otherwise be counted in one list and not the other.
		snapshot := entries()
		scans := findLog(snapshot, "scan_complete")
		findings := findLog(snapshot, "suspected_finding")

		for _, c := range scans {
			Expect(c.attrs["suspected"]).To(BeNumerically("==", 1))
		}
		Expect(len(findings)).To(BeNumerically(">=", len(scans)))

		for _, f := range findings {
			Expect(f.attrs["vmi_uid"]).To(Equal("uid-1"))
			Expect(f.attrs["classification"]).To(Equal(string(domain.SuspectedKernelPanic)))
		}
	})

	It("defers a target whose node permit is busy rather than dropping it", func() {
		vmis := []kubevirtv1.VirtualMachineInstance{
			eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1"),
			eligibleVMI("ns-a", "vmi-2", "node-1", "uid-2"),
			eligibleVMI("ns-a", "vmi-3", "node-1", "uid-3"),
		}
		client := singleNamespaceClient(vmis...)
		console := &fakeConsole{delay: 30 * time.Millisecond}
		classifier := &fakeClassifier{result: domain.ClassificationResult{
			Classification: domain.NoTargetFailureVisible,
			ReasonCode:     domain.NoFailureVisible,
		}}
		logger, entries := newCaptureLogger()
		cfg := baseScanConfig()
		cfg.Scan.PerNodeConcurrency = 1
		cfg.Scan.FirstPassDeadline = 2 * time.Second

		scanner, err := agent.NewScanner(cfg, agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() []logEntry { return findLog(entries(), "scan_complete") }, "5s").Should(HaveLen(1))
		sc := findLog(entries(), "scan_complete")[0]
		Expect(sc.attrs["captured"]).To(BeNumerically("==", 3))
		Expect(sc.attrs["skipped"]).To(BeNumerically("==", 0))
		Expect(console.captureCount()).To(Equal(3))
	})

	It("counts a target still pending once the first-pass deadline fires as skipped", func() {
		vmis := []kubevirtv1.VirtualMachineInstance{
			eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1"),
			eligibleVMI("ns-a", "vmi-2", "node-1", "uid-2"),
			eligibleVMI("ns-a", "vmi-3", "node-1", "uid-3"),
		}
		client := singleNamespaceClient(vmis...)
		console := &fakeConsole{delay: 200 * time.Millisecond}
		classifier := &fakeClassifier{result: domain.ClassificationResult{
			Classification: domain.NoTargetFailureVisible,
			ReasonCode:     domain.NoFailureVisible,
		}}
		logger, entries := newCaptureLogger()
		cfg := baseScanConfig()
		cfg.Scan.PerNodeConcurrency = 1
		cfg.Scan.FirstPassDeadline = 250 * time.Millisecond

		scanner, err := agent.NewScanner(cfg, agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() []logEntry { return findLog(entries(), "scan_complete") }, "3s").Should(HaveLen(1))
		sc := findLog(entries(), "scan_complete")[0]
		Expect(sc.attrs["skipped"]).To(BeNumerically(">", 0))
		Expect(asInt(sc.attrs["captured"]) + asInt(sc.attrs["skipped"]) + asInt(sc.attrs["errors"])).To(Equal(3))
	})

	It("stops promptly when the context is cancelled", func() {
		client := singleNamespaceClient()
		console := &fakeConsole{}
		classifier := &fakeClassifier{}
		logger, _ := newCaptureLogger()

		scanner, err := agent.NewScanner(baseScanConfig(), agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- scanner.Run(ctx) }()
		cancel()

		Eventually(done, "1s").Should(Receive(MatchError(context.Canceled)))
	})

	It("records every classifier failure as its own coverage gap", func() {
		vmis := make([]kubevirtv1.VirtualMachineInstance, 5)
		for i := range vmis {
			vmis[i] = eligibleVMI("ns-a", string(rune('a'+i))+"-vmi", "node-1", types.UID(string(rune('a'+i))))
		}
		client := singleNamespaceClient(vmis...)
		console := &fakeConsole{}
		classifier := &fakeClassifier{err: errors.New("provider down")}
		logger, entries := newCaptureLogger()
		cfg := baseScanConfig()
		cfg.Scan.PerNodeConcurrency = 5

		scanner, err := agent.NewScanner(cfg, agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() []logEntry { return findLog(entries(), "scan_complete") }, "5s").Should(HaveLen(1))

		// There is no in-scan provider cutoff, so every target pays for its
		// own failed model call: five targets, five classifier calls, five
		// coverage gaps.
		Expect(classifier.calls()).To(Equal(5))
		gaps := findLog(entries(), "coverage_gap")
		Expect(gaps).To(HaveLen(5))
		for _, g := range gaps {
			Expect(g.attrs["stage"]).To(Equal(string(domain.StageClassifier)))
			Expect(g.attrs["bounded_error_code"]).To(Equal(string(domain.ErrUnavailable)))
		}

		sc := findLog(entries(), "scan_complete")[0]
		Expect(sc.attrs["errors"]).To(BeNumerically("==", 5))
		Expect(sc.attrs["classified"]).To(BeNumerically("==", 0))
	})

	It("records one observation per classified target, including healthy ones", func() {
		vmi := eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1")
		client := singleNamespaceClient(vmi)
		console := &fakeConsole{}
		classifier := &fakeClassifier{result: domain.ClassificationResult{
			Classification: domain.NoTargetFailureVisible,
			ReasonCode:     domain.NoFailureVisible,
		}}
		logger, entries := newCaptureLogger()
		recorder := &fakeRecorder{}

		scanner, err := agent.NewScanner(baseScanConfig(), agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger, Recorder: recorder,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() []logEntry { return findLog(entries(), "scan_complete") }, "5s").Should(HaveLen(1))

		obs, scans := recorder.snapshot()
		Expect(obs).To(HaveLen(1))
		Expect(obs[0].Outcome).To(Equal(domain.OutcomeClassified))
		Expect(obs[0].Classification).To(Equal(domain.NoTargetFailureVisible))
		Expect(obs[0].Namespace).To(Equal("ns-a"))
		Expect(obs[0].Name).To(Equal("vmi-1"))
		Expect(scans).To(HaveLen(1))
		Expect(scans[0].Selected).To(Equal(1))
		Expect(scans[0].Classified).To(BeNumerically("==", 1))
	})

	It("records no observation for a namespace-scoped discovery failure", func() {
		client := &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
			"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				return nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "virtualmachineinstances"}, "", errors.New("no rbac"))
			}},
		}}
		console := &fakeConsole{}
		classifier := &fakeClassifier{}
		logger, entries := newCaptureLogger()
		recorder := &fakeRecorder{}

		scanner, err := agent.NewScanner(baseScanConfig(), agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger, Recorder: recorder,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() []logEntry { return findLog(entries(), "scan_complete") }, "5s").Should(HaveLen(1))

		// A namespace-scoped discovery failure has no VMI identity to key
		// an observation by, so unlike a per-target failure it must not
		// reach the Recorder even though it does still count as a
		// coverage gap and a scan error.
		gaps := findLog(entries(), "coverage_gap")
		Expect(gaps).To(HaveLen(1))
		Expect(gaps[0].attrs["stage"]).To(Equal(string(domain.StageDiscovery)))

		obs, scans := recorder.snapshot()
		Expect(obs).To(BeEmpty())
		Expect(scans).To(HaveLen(1))
		Expect(scans[0].Errors).To(BeNumerically("==", 1))
	})

	It("removes a dashboard entry once its VMI is deleted", func() {
		var mu sync.Mutex
		items := []kubevirtv1.VirtualMachineInstance{eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1")}
		client := &fakeVMIClient{byNamespace: map[string]*fakeVMIInterface{
			"ns-a": {listFunc: func(ctx context.Context, opts metav1.ListOptions) (*kubevirtv1.VirtualMachineInstanceList, error) {
				mu.Lock()
				defer mu.Unlock()
				return &kubevirtv1.VirtualMachineInstanceList{Items: items}, nil
			}},
		}}
		console := &fakeConsole{}
		classifier := &fakeClassifier{result: domain.ClassificationResult{
			Classification: domain.NoTargetFailureVisible,
			ReasonCode:     domain.NoFailureVisible,
		}}
		logger, _ := newCaptureLogger()
		store := dashboard.NewStore(10)
		cfg := baseScanConfig()
		cfg.Scan.Interval = 20 * time.Millisecond

		scanner, err := agent.NewScanner(cfg, agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger, Recorder: store,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() int { return len(store.Snapshot().Observations) }, "2s").Should(Equal(1))

		mu.Lock()
		items = nil
		mu.Unlock()

		Eventually(func() int { return len(store.Snapshot().Observations) }, "2s").Should(Equal(0))
	})

	It("records one failed observation per screenshot failure", func() {
		vmi := eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1")
		client := singleNamespaceClient(vmi)
		console := &fakeConsole{err: errors.New("console unavailable")}
		classifier := &fakeClassifier{}
		logger, entries := newCaptureLogger()
		recorder := &fakeRecorder{}

		scanner, err := agent.NewScanner(baseScanConfig(), agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger, Recorder: recorder,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		Eventually(func() []logEntry { return findLog(entries(), "scan_complete") }, "5s").Should(HaveLen(1))

		obs, _ := recorder.snapshot()
		Expect(obs).To(HaveLen(1))
		Expect(obs[0].Outcome).To(Equal(domain.OutcomeFailed))
		Expect(obs[0].Stage).To(Equal(domain.StageScreenshot))
		Expect(classifier.calls()).To(Equal(0))
	})

	It("rejects deps missing a required collaborator", func() {
		_, err := agent.NewScanner(baseScanConfig(), agent.ScannerOptions{})
		Expect(err).To(HaveOccurred())
	})

	It("releases a node's screenshot permit before the classifier call (C2)", func() {
		vmis := []kubevirtv1.VirtualMachineInstance{
			eligibleVMI("ns-a", "vmi-1", "node-1", "uid-1"),
			eligibleVMI("ns-a", "vmi-2", "node-1", "uid-2"),
		}
		client := singleNamespaceClient(vmis...)
		console := &fakeConsole{}

		classifier := &blockingClassifier{release: make(chan struct{})}
		logger, _ := newCaptureLogger()
		cfg := baseScanConfig()
		cfg.Scan.PerNodeConcurrency = 1
		cfg.Scan.FirstPassDeadline = 3 * time.Second

		scanner, err := agent.NewScanner(cfg, agent.ScannerOptions{
			VMIClient: client, Console: console, Classifier: classifier, Logger: logger,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go scanner.Run(ctx)

		// perNodeConcurrency is 1, so if the permit were (incorrectly) held
		// across the classifier call, the second target's screenshot could
		// never be captured while the first target's classify call is
		// blocked. Both classify calls entering concurrently proves the
		// permit was released after capture, not after classification.
		Eventually(classifier.inFlight, "2s").Should(BeNumerically("==", 2))
		Expect(console.captureCount()).To(Equal(2))
		close(classifier.release)
	})
})

// blockingClassifier blocks every Classify call until release is closed,
// tracking how many calls are concurrently in flight.
type blockingClassifier struct {
	release chan struct{}
	count   atomic.Int32
}

func (c *blockingClassifier) Classify(ctx context.Context, req domain.ClassifyRequest) (domain.ClassificationResult, domain.Usage, error) {
	c.count.Add(1)
	defer c.count.Add(-1)
	select {
	case <-c.release:
	case <-ctx.Done():
	}
	return domain.ClassificationResult{Classification: domain.NoTargetFailureVisible, ReasonCode: domain.NoFailureVisible}, domain.Usage{}, nil
}

func (c *blockingClassifier) inFlight() int32 { return c.count.Load() }
