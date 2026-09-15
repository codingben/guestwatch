package domain

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

// Stage names a point in the per-target pipeline, used to label coverage
// gaps and namespace failures.
type Stage string

const (
	StageDiscovery  Stage = "DISCOVERY"
	StageScreenshot Stage = "SCREENSHOT"
	StageClassifier Stage = "CLASSIFIER"
	StageTriage     Stage = "TRIAGE"
)

// ErrorCode is a bounded, loggable classification of a failure. It is never
// a raw error string, so it can appear in structured logs unbounded in
// volume without leaking provider payloads or stack traces.
type ErrorCode string

const (
	ErrStaleTarget       ErrorCode = "STALE_TARGET"
	ErrPermission        ErrorCode = "PERMISSION_DENIED"
	ErrTimeout           ErrorCode = "TIMEOUT"
	ErrUnavailable       ErrorCode = "SERVICE_UNAVAILABLE"
	ErrMalformedResponse ErrorCode = "MALFORMED_RESPONSE"
	ErrEvidenceLimit     ErrorCode = "EVIDENCE_LIMIT"
	ErrListExpired       ErrorCode = "LIST_EXPIRED"
	ErrCancelled         ErrorCode = "CANCELLED"
)

// Classification is the model's four-state verdict for one screenshot.
type Classification string

const (
	NoTargetFailureVisible Classification = "NO_TARGET_FAILURE_VISIBLE"
	Unknown                Classification = "UNKNOWN"
	SuspectedKernelPanic   Classification = "SUSPECTED_KERNEL_PANIC"
	SuspectedWindowsBSOD   Classification = "SUSPECTED_WINDOWS_BSOD"
)

// ReasonCode explains a Classification. Not every ReasonCode is valid with
// every Classification; see model.ValidPair.
type ReasonCode string

const (
	NoFailureVisible          ReasonCode = "NO_FAILURE_VISIBLE"
	KernelPanicVisible        ReasonCode = "KERNEL_PANIC_VISIBLE"
	WindowsBSODVisible        ReasonCode = "WINDOWS_BSOD_VISIBLE"
	BlankOrUnreadable         ReasonCode = "BLANK_OR_UNREADABLE"
	FirmwareInstallerRecovery ReasonCode = "FIRMWARE_INSTALLER_RECOVERY"
	AmbiguousOrCropped        ReasonCode = "AMBIGUOUS_OR_CROPPED"
	TextTooSmall              ReasonCode = "TEXT_TOO_SMALL"
)

// ClassificationResult is the strict, fixed-shape output the model must
// produce. It carries no confidence field, free-form summary, or tool call:
// classification and reason code are the entire output contract.
type ClassificationResult struct {
	Classification Classification `json:"classification"`
	ReasonCode     ReasonCode     `json:"reason_code"`
}

// Target is one eligible, UID-bound VMI selected for this scan.
type Target struct {
	Namespace string
	Name      string
	Node      string
	UID       types.UID
}

// Usage is the token accounting for one classifier call. It is aggregated
// (never per-response) into scan_complete so that §8's cost baseline can be
// measured from the structured logs.
type Usage struct {
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
}

// Screenshot is the PNG evidence captured for one Target.
type Screenshot struct {
	PNG        []byte
	CapturedAt time.Time
}

type Outcome string

const (
	OutcomeClassified Outcome = "classified"
	OutcomeFailed     Outcome = "failed"
)

// Observation is one VMI's outcome from a single scan pass, fed to the
// dashboard's Recorder.
type Observation struct {
	Namespace      string         `json:"namespace"`
	Name           string         `json:"name"`
	Node           string         `json:"node"`
	UID            types.UID      `json:"uid"`
	ScanID         string         `json:"scanId"`
	ObservedAt     time.Time      `json:"observedAt"`
	Outcome        Outcome        `json:"outcome"`
	Classification Classification `json:"classification,omitempty"`
	ReasonCode     ReasonCode     `json:"reasonCode,omitempty"`
	Stage          Stage          `json:"stage,omitempty"`
	ErrorCode      ErrorCode      `json:"errorCode,omitempty"`
}

// ScanRecord is the scan-level outcome exposed on the dashboard API,
// mirroring the scan_complete log line.
type ScanRecord struct {
	ScanID     string    `json:"scanId"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMS int64     `json:"durationMs"`
	Namespaces int       `json:"namespaces"`
	Selected   int       `json:"selected"`
	Captured   int64     `json:"captured"`
	Classified int64     `json:"classified"`
	Skipped    int64     `json:"skipped"`
	Errors     int64     `json:"errors"`
	Overrun    bool      `json:"overrun"`
}

// ClassifyRequest is the sole input to a Classifier.
type ClassifyRequest struct {
	Target     Target
	Screenshot Screenshot
}

// TargetError is a bounded, loggable per-target failure. It is the only
// error type that crosses the mcp and agent package boundary, so a
// coverage_gap record always has a concrete ErrorCode to log rather than a
// raw, unbounded error string.
type TargetError struct {
	Code ErrorCode
	Msg  string
}

func (e *TargetError) Error() string { return e.Msg }

// Eligible reports whether vmi satisfies every predicate required before a
// screenshot may be captured:
//
//   - status.phase == Running and status.nodeName is non-empty;
//   - spec.domain.devices.autoattachGraphicsDevice is nil or true;
//   - the Paused condition is absent or not ConditionTrue; and
//   - migrationState is nil or has a non-nil EndTimestamp (a StartTimestamp
//     set with no EndTimestamp is an active migration and is excluded).
//
// This is the single definition of eligibility: it is evaluated once at
// discovery time and re-evaluated once, unchanged, by the Console MCP
// client immediately before capture.
func Eligible(vmi *kubevirtv1.VirtualMachineInstance) bool {
	if vmi == nil {
		return false
	}
	if vmi.Status.Phase != kubevirtv1.Running {
		return false
	}
	if vmi.Status.NodeName == "" {
		return false
	}
	if g := vmi.Spec.Domain.Devices.AutoattachGraphicsDevice; g != nil && !*g {
		return false
	}
	for _, cond := range vmi.Status.Conditions {
		if cond.Type == kubevirtv1.VirtualMachineInstancePaused && cond.Status == corev1.ConditionTrue {
			return false
		}
	}
	if ms := vmi.Status.MigrationState; ms != nil && ms.EndTimestamp == nil {
		return false
	}
	return true
}

// VMIKey is the sole definition of the string identity used to correlate a
// VMI across discovery and the dashboard store: both must agree on this
// format for pruning of deleted VMIs to work.
func VMIKey(namespace, name string) string {
	return namespace + "/" + name
}

// TriageTool names one of the three read-only Console MCP tools the triage
// model may call.
type TriageTool string

const (
	ToolConsoleScreenshot TriageTool = "console_screenshot"
	ToolConsoleLog        TriageTool = "console_log"
	ToolConsoleCapture    TriageTool = "console_capture"
)

// SuspectedCause is the triage model's judgment of what most likely
// explains the VM's state.
type SuspectedCause string

const (
	CauseKernelPanic    SuspectedCause = "KERNEL_PANIC"
	CauseWindowsBSOD    SuspectedCause = "WINDOWS_BSOD"
	CauseBootFailure    SuspectedCause = "BOOT_FAILURE"
	CauseStorageFailure SuspectedCause = "STORAGE_FAILURE"
	CauseOutOfMemory    SuspectedCause = "OUT_OF_MEMORY"
	CauseGuestHung      SuspectedCause = "GUEST_HUNG"
	CauseNoFailureFound SuspectedCause = "NO_FAILURE_FOUND"
	CauseIndeterminate  SuspectedCause = "INDETERMINATE"
)

// Confidence is the triage model's self-assessed confidence in its
// SuspectedCause.
type Confidence string

const (
	ConfidenceLow    Confidence = "LOW"
	ConfidenceMedium Confidence = "MEDIUM"
	ConfidenceHigh   Confidence = "HIGH"
)

// TriageRequest is the input to a triage investigation.
type TriageRequest struct {
	Target             Target
	Classification     Classification
	ReasonCode         ReasonCode
	ClassificationTime time.Time
}

// TriageResult is the triage model's structured investigation output.
// Every text field is length-bounded by the caller before it is stored or
// rendered, since it is derived from untrusted guest-controlled console
// output.
type TriageResult struct {
	SuspectedCause SuspectedCause `json:"suspectedCause"`
	Confidence     Confidence     `json:"confidence"`
	Summary        string         `json:"summary"`
	KeyEvidence    []string       `json:"keyEvidence"`
	NextSteps      []string       `json:"nextSteps"`

	// Filled in by the tool loop from what it actually observed, not from
	// the model's own claims.
	ToolsUsed     []TriageTool `json:"toolsUsed"`
	ToolCallCount int          `json:"toolCallCount"`
}

// TriageRecord is one on-demand investigation, attached to the store
// Entry for the VM it investigated.
type TriageRecord struct {
	Namespace   string        `json:"namespace"`
	Name        string        `json:"name"`
	UID         types.UID     `json:"uid"`
	RequestedAt time.Time     `json:"requestedAt"`
	DurationMS  int64         `json:"durationMs"`
	Result      *TriageResult `json:"result,omitempty"`
	ErrorCode   ErrorCode     `json:"errorCode,omitempty"`
	Usage       Usage         `json:"usage"`
}
