package model

import (
	"context"
	"sort"
	"unicode/utf8"

	"github.com/codingben/guestwatch/internal/domain"
)

type Classifier interface {
	Classify(ctx context.Context, req domain.ClassifyRequest) (domain.ClassificationResult, domain.Usage, error)
}

type ToolRunner interface {
	Screenshot(ctx context.Context, target domain.Target) (domain.Screenshot, error)
	ConsoleLog(ctx context.Context, target domain.Target, tailLines int, previous bool) (string, error)
	ConsoleCapture(ctx context.Context, target domain.Target, durationSeconds int) (string, error)
}

var validReasonCodes = map[domain.Classification]map[domain.ReasonCode]bool{
	domain.NoTargetFailureVisible: {
		domain.NoFailureVisible:          true,
		domain.FirmwareInstallerRecovery: true,
	},
	domain.SuspectedKernelPanic: {
		domain.KernelPanicVisible: true,
	},
	domain.SuspectedWindowsBSOD: {
		domain.WindowsBSODVisible: true,
	},
	domain.Unknown: {
		domain.BlankOrUnreadable:  true,
		domain.AmbiguousOrCropped: true,
		domain.TextTooSmall:       true,
	},
}

func ValidPair(class domain.Classification, reason domain.ReasonCode) bool {
	reasons, ok := validReasonCodes[class]
	if !ok {
		return false
	}
	return reasons[reason]
}

var classificationSchema = buildClassificationSchema()

func buildClassificationSchema() map[string]any {
	classes := make([]string, 0, len(validReasonCodes))
	reasonSet := make(map[domain.ReasonCode]struct{})
	for class, reasons := range validReasonCodes {
		classes = append(classes, string(class))
		for reason := range reasons {
			reasonSet[reason] = struct{}{}
		}
	}
	sort.Strings(classes)

	reasons := make([]string, 0, len(reasonSet))
	for reason := range reasonSet {
		reasons = append(reasons, string(reason))
	}
	sort.Strings(reasons)

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"classification": map[string]any{"type": "string", "enum": classes},
			"reason_code":    map[string]any{"type": "string", "enum": reasons},
		},
		"required":             []string{"classification", "reason_code"},
		"additionalProperties": false,
	}
}

const (
	maxTriageSummaryChars  = 1500
	maxTriageListItems     = 10
	maxTriageListItemChars = 300
)

var validSuspectedCauses = map[domain.SuspectedCause]bool{
	domain.CauseKernelPanic:    true,
	domain.CauseWindowsBSOD:    true,
	domain.CauseBootFailure:    true,
	domain.CauseStorageFailure: true,
	domain.CauseOutOfMemory:    true,
	domain.CauseGuestHung:      true,
	domain.CauseNoFailureFound: true,
	domain.CauseIndeterminate:  true,
}

var validConfidences = map[domain.Confidence]bool{
	domain.ConfidenceLow:    true,
	domain.ConfidenceMedium: true,
	domain.ConfidenceHigh:   true,
}

var triageResultSchema = buildTriageResultSchema()

func buildTriageResultSchema() map[string]any {
	causes := make([]string, 0, len(validSuspectedCauses))
	for c := range validSuspectedCauses {
		causes = append(causes, string(c))
	}
	sort.Strings(causes)

	confidences := make([]string, 0, len(validConfidences))
	for c := range validConfidences {
		confidences = append(confidences, string(c))
	}
	sort.Strings(confidences)

	stringArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"suspectedCause": map[string]any{"type": "string", "enum": causes},
			"confidence":     map[string]any{"type": "string", "enum": confidences},
			"summary":        map[string]any{"type": "string"},
			"keyEvidence":    stringArray,
			"nextSteps":      stringArray,
		},
		// toolsUsed is not model-facing: the tool loop fills it in itself.
		"required":             []string{"suspectedCause", "confidence", "summary", "keyEvidence", "nextSteps"},
		"additionalProperties": false,
	}
}

func boundTriageResult(result domain.TriageResult) domain.TriageResult {
	result.Summary = truncateString(result.Summary, maxTriageSummaryChars)
	result.KeyEvidence = truncateStringList(result.KeyEvidence, maxTriageListItems, maxTriageListItemChars)
	result.NextSteps = truncateStringList(result.NextSteps, maxTriageListItems, maxTriageListItemChars)
	return result
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	b := s[:max]
	for len(b) > 0 {
		r, size := utf8.DecodeLastRuneInString(b)
		if r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-size]
	}
	return b
}

func truncateStringList(list []string, maxItems, maxItemChars int) []string {
	if len(list) > maxItems {
		list = list[:maxItems]
	}
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = truncateString(s, maxItemChars)
	}
	return out
}

func ValidTriageResult(result domain.TriageResult) bool {
	return validSuspectedCauses[result.SuspectedCause] && validConfidences[result.Confidence]
}
