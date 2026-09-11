package agent

import (
	"sync/atomic"
	"time"

	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

type ScanSummary struct {
	ScanID     string
	StartedAt  time.Time
	Duration   time.Duration
	Namespaces int
	Selected   int
	Overrun    bool

	Captured   atomic.Int64
	Classified atomic.Int64
	Skipped    atomic.Int64
	Errors     atomic.Int64

	NoTargetFailure atomic.Int64
	Unknown         atomic.Int64
	Suspected       atomic.Int64

	InputTokens     atomic.Int64
	OutputTokens    atomic.Int64
	ReasoningTokens atomic.Int64
}

func (s *ScanSummary) recordUsage(u domain.Usage) {
	s.InputTokens.Add(u.InputTokens)
	s.OutputTokens.Add(u.OutputTokens)
	s.ReasoningTokens.Add(u.ReasoningTokens)
}

func (s *ScanSummary) recordClassification(c domain.Classification) {
	switch c {
	case domain.NoTargetFailureVisible:
		s.NoTargetFailure.Add(1)
	case domain.Unknown:
		s.Unknown.Add(1)
	default:
		s.Suspected.Add(1)
	}
}
