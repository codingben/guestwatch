package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/codingben/kubevirt-ai-agent/internal/config"
	"github.com/codingben/kubevirt-ai-agent/internal/domain"
	"github.com/codingben/kubevirt-ai-agent/internal/model"
)

type ConsoleClient interface {
	Screenshot(ctx context.Context, target domain.Target) (domain.Screenshot, error)
}

type Recorder interface {
	RecordObservation(domain.Observation)
	RecordScan(domain.ScanRecord)

	Prune(listedNamespaces []string, live map[string]struct{})
}

type nopRecorder struct{}

func (nopRecorder) RecordObservation(domain.Observation) {}
func (nopRecorder) RecordScan(domain.ScanRecord)         {}
func (nopRecorder) Prune([]string, map[string]struct{})  {}

type ScannerOptions struct {
	VMIClient  VMIClient
	Console    ConsoleClient
	Classifier model.Classifier
	Logger     *slog.Logger
	Recorder   Recorder
}

type Scanner struct {
	cfg      config.Config
	deps     ScannerOptions
	selector labels.Selector
}

func NewScanner(cfg config.Config, deps ScannerOptions) (*Scanner, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.VMIClient == nil || deps.Console == nil || deps.Classifier == nil || deps.Logger == nil {
		return nil, errors.New("agent: ScannerOptions.VMIClient, Console, Classifier, and Logger are required")
	}

	selector := labels.Everything()
	if cfg.Scan.LabelSelector != "" {
		parsed, err := labels.Parse(cfg.Scan.LabelSelector)
		if err != nil {
			return nil, err
		}
		selector = parsed
	}

	if deps.Recorder == nil {
		deps.Recorder = nopRecorder{}
	}

	return &Scanner{
		cfg:      cfg,
		deps:     deps,
		selector: selector,
	}, nil
}

func (s *Scanner) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.Scan.Interval)
	defer ticker.Stop()

	for {
		s.scanOnce(ctx)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Scanner) scanOnce(ctx context.Context) {
	start := time.Now()
	run := &scanRun{Scanner: s, summary: &ScanSummary{
		ScanID:     start.UTC().Format(time.RFC3339Nano),
		StartedAt:  start,
		Namespaces: len(s.cfg.Scan.Namespaces),
	}}

	defer func() {
		run.summary.Duration = time.Since(start)
		run.summary.Overrun = run.summary.Duration >= s.cfg.Scan.Interval
		run.logScanComplete()
		run.deps.Recorder.RecordScan(domain.ScanRecord{
			ScanID:     run.summary.ScanID,
			StartedAt:  run.summary.StartedAt,
			DurationMS: run.summary.Duration.Milliseconds(),
			Namespaces: run.summary.Namespaces,
			Selected:   run.summary.Selected,
			Captured:   run.summary.Captured.Load(),
			Classified: run.summary.Classified.Load(),
			Skipped:    run.summary.Skipped.Load(),
			Errors:     run.summary.Errors.Load(),
			Overrun:    run.summary.Overrun,
		})
	}()

	discovery := ListTargets(ctx, s.deps.VMIClient, s.cfg.Scan.Namespaces, s.selector)

	s.deps.Recorder.Prune(discovery.ListedNamespaces, discovery.Live)

	for _, f := range discovery.Failures {
		run.fail(f.Namespace, "", f.Stage, f.Code)
	}

	run.summary.Selected = len(discovery.Targets)
	run.process(ctx, discovery.Targets)
}

type scanRun struct {
	*Scanner
	summary *ScanSummary
}

func (r *scanRun) process(ctx context.Context, targets []domain.Target) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.Scan.FirstPassDeadline)
	defer cancel()

	queue := make(chan domain.Target, len(targets))
	permits := make(map[string]chan struct{})
	for _, t := range targets {
		queue <- t
		if _, ok := permits[t.Node]; !ok {
			permits[t.Node] = make(chan struct{}, r.cfg.Scan.PerNodeConcurrency)
		}
	}
	close(queue)

	var wg sync.WaitGroup
	for range r.cfg.Scan.WorkerCount {
		wg.Go(func() {
			for target := range queue {
				if ctx.Err() != nil {
					return
				}
				permit := permits[target.Node]
				select {
				case permit <- struct{}{}:
				case <-ctx.Done():
					return
				}
				r.processTarget(ctx, target, permit)
			}
		})
	}
	wg.Wait()

	processed := r.summary.Captured.Load() + r.summary.Errors.Load()
	r.summary.Skipped.Add(int64(len(targets)) - processed)
}

func (r *scanRun) processTarget(ctx context.Context, target domain.Target, permit chan struct{}) {
	shot, err := r.deps.Console.Screenshot(ctx, target)

	<-permit
	if err != nil {
		r.failTarget(target, domain.StageScreenshot, errorCode(err))
		return
	}

	r.summary.Captured.Add(1)
	result, usage, err := r.deps.Classifier.Classify(ctx, domain.ClassifyRequest{Target: target, Screenshot: shot})
	r.summary.recordUsage(usage)

	if err != nil {
		r.failTarget(target, domain.StageClassifier, errorCode(err))
		return
	}

	r.summary.Classified.Add(1)
	r.record(target, result, shot.CapturedAt)
}

func (r *scanRun) record(target domain.Target, result domain.ClassificationResult, capturedAt time.Time) {
	r.summary.recordClassification(result.Classification)

	r.deps.Recorder.RecordObservation(domain.Observation{
		Namespace:      target.Namespace,
		Name:           target.Name,
		Node:           target.Node,
		UID:            target.UID,
		ScanID:         r.summary.ScanID,
		ObservedAt:     capturedAt,
		Outcome:        domain.OutcomeClassified,
		Classification: result.Classification,
		ReasonCode:     result.ReasonCode,
	})

	switch result.Classification {
	case domain.NoTargetFailureVisible:
		// Healthy: counted in the summary, never logged per target.
	case domain.Unknown:
		r.logUnknownResult(target, result.ReasonCode)
	default:
		r.logSuspectedFinding(target, result, capturedAt, time.Since(capturedAt))
	}
}

func (r *scanRun) fail(namespace, vmiUID string, stage domain.Stage, code domain.ErrorCode) {
	r.logCoverageGap(namespace, vmiUID, stage, code)
	r.summary.Errors.Add(1)
}

func (r *scanRun) failTarget(target domain.Target, stage domain.Stage, code domain.ErrorCode) {
	r.fail(target.Namespace, string(target.UID), stage, code)

	r.deps.Recorder.RecordObservation(domain.Observation{
		Namespace:  target.Namespace,
		Name:       target.Name,
		Node:       target.Node,
		UID:        target.UID,
		ScanID:     r.summary.ScanID,
		ObservedAt: time.Now(),
		Outcome:    domain.OutcomeFailed,
		Stage:      stage,
		ErrorCode:  code,
	})
}

func errorCode(err error) domain.ErrorCode {
	var te *domain.TargetError
	if errors.As(err, &te) {
		return te.Code
	}
	return domain.ErrUnavailable
}
