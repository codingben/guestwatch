package agent

import (
	"time"

	"github.com/codingben/guestwatch/internal/domain"
	"github.com/codingben/guestwatch/internal/model"
)

func (r *scanRun) logScanComplete() {
	sum := r.summary
	r.deps.Logger.Info("scan_complete",
		"scan_id", sum.ScanID,
		"started_at", sum.StartedAt.UTC().Format(time.RFC3339),
		"duration_ms", sum.Duration.Milliseconds(),
		"namespaces", sum.Namespaces,
		"selected", sum.Selected,
		"captured", sum.Captured.Load(),
		"classified", sum.Classified.Load(),
		"no_target_failure", sum.NoTargetFailure.Load(),
		"unknown", sum.Unknown.Load(),
		"suspected", sum.Suspected.Load(),
		"skipped", sum.Skipped.Load(),
		"errors", sum.Errors.Load(),
		"overrun", sum.Overrun,
		"input_tokens", sum.InputTokens.Load(),
		"output_tokens", sum.OutputTokens.Load(),
		"reasoning_tokens", sum.ReasoningTokens.Load(),
	)
}

func (r *scanRun) logSuspectedFinding(target domain.Target, result domain.ClassificationResult, capturedAt time.Time, duration time.Duration) {
	r.deps.Logger.Warn("suspected_finding",
		"scan_id", r.summary.ScanID,
		"namespace", target.Namespace,
		"vmi", target.Name,
		"vmi_uid", string(target.UID),
		"node", target.Node,
		"classification", string(result.Classification),
		"reason_code", string(result.ReasonCode),
		"prompt_version", model.PromptVersion,
		"captured_at", capturedAt.UTC().Format(time.RFC3339),
		"duration_ms", duration.Milliseconds(),
	)
}

func (r *scanRun) logUnknownResult(target domain.Target, reason domain.ReasonCode) {
	r.deps.Logger.Info("unknown_result",
		"scan_id", r.summary.ScanID,
		"namespace", target.Namespace,
		"vmi", target.Name,
		"vmi_uid", string(target.UID),
		"reason_code", string(reason),
	)
}

func (r *scanRun) logCoverageGap(namespace, vmiUID string, stage domain.Stage, code domain.ErrorCode) {
	r.deps.Logger.Warn("coverage_gap",
		"scan_id", r.summary.ScanID,
		"namespace", namespace,
		"vmi_uid", vmiUID,
		"stage", string(stage),
		"bounded_error_code", string(code),
	)
}
