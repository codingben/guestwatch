package model

import (
	"context"
	"sort"

	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

type Classifier interface {
	Classify(ctx context.Context, req domain.ClassifyRequest) (domain.ClassificationResult, domain.Usage, error)
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
