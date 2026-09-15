package dashboard_test

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/codingben/kubevirt-ai-agent/internal/dashboard"
	"github.com/codingben/kubevirt-ai-agent/internal/domain"
)

func classifiedObservation(namespace, name string, observedAt time.Time) domain.Observation {
	return domain.Observation{
		Namespace:      namespace,
		Name:           name,
		Node:           "node-1",
		ScanID:         "scan-1",
		ObservedAt:     observedAt,
		Outcome:        domain.OutcomeClassified,
		Classification: domain.NoTargetFailureVisible,
		ReasonCode:     domain.NoFailureVisible,
	}
}

var _ = Describe("Store", func() {
	It("reports a non-nil, empty observation list before anything is recorded", func() {
		store := dashboard.NewStore(10)
		snap := store.Snapshot()
		Expect(snap.LastScan).To(BeNil())
		Expect(snap.Observations).NotTo(BeNil())
		Expect(snap.Observations).To(BeEmpty())
	})

	It("keeps only the latest observation for a repeated VMI", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now.Add(time.Minute)))

		snap := store.Snapshot()
		Expect(snap.Observations).To(HaveLen(1))
		Expect(snap.Observations[0].UpdatedAt).To(BeTemporally("==", now.Add(time.Minute)))
	})

	It("evicts the least-recently-updated entry once at capacity", func() {
		store := dashboard.NewStore(2)
		base := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", base))
		store.RecordObservation(classifiedObservation("ns-a", "vmi-2", base.Add(time.Minute)))
		store.RecordObservation(classifiedObservation("ns-a", "vmi-3", base.Add(2*time.Minute)))

		snap := store.Snapshot()
		Expect(snap.Observations).To(HaveLen(2))
		names := []string{snap.Observations[0].Name, snap.Observations[1].Name}
		Expect(names).To(ConsistOf("vmi-2", "vmi-3"))
	})

	It("stays at capacity when a stable, over-capacity fleet is re-recorded every scan", func() {
		// A fleet of 3 distinct VMIs, capped to 2, scanned repeatedly:
		// each scan evicts and re-inserts a VMI that was seen before, so
		// nothing may accumulate across scans.
		store := dashboard.NewStore(2)
		base := time.Now()
		for scan := 0; scan < 5; scan++ {
			for i, name := range []string{"vmi-1", "vmi-2", "vmi-3"} {
				offset := time.Duration(scan*3+i) * time.Second
				store.RecordObservation(classifiedObservation("ns-a", name, base.Add(offset)))
			}
		}

		snap := store.Snapshot()
		Expect(snap.Observations).To(HaveLen(2))
	})

	It("sorts observations by namespace then name", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-b", "vmi-1", now))
		store.RecordObservation(classifiedObservation("ns-a", "vmi-2", now))
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))

		snap := store.Snapshot()
		Expect(snap.Observations).To(HaveLen(3))
		Expect(snap.Observations[0].Namespace).To(Equal("ns-a"))
		Expect(snap.Observations[0].Name).To(Equal("vmi-1"))
		Expect(snap.Observations[1].Namespace).To(Equal("ns-a"))
		Expect(snap.Observations[1].Name).To(Equal("vmi-2"))
		Expect(snap.Observations[2].Namespace).To(Equal("ns-b"))
	})

	It("keeps the last classification and the last failure independently", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))
		store.RecordObservation(domain.Observation{
			Namespace:  "ns-a",
			Name:       "vmi-1",
			ScanID:     "scan-2",
			ObservedAt: now.Add(time.Minute),
			Outcome:    domain.OutcomeFailed,
			Stage:      domain.StageScreenshot,
			ErrorCode:  domain.ErrUnavailable,
		})

		snap := store.Snapshot()
		Expect(snap.Observations).To(HaveLen(1))
		entry := snap.Observations[0]
		Expect(entry.LastClassification).NotTo(BeNil())
		Expect(entry.LastClassification.Classification).To(Equal(domain.NoTargetFailureVisible))
		Expect(entry.LastFailure).NotTo(BeNil())
		Expect(entry.LastFailure.ErrorCode).To(Equal(domain.ErrUnavailable))
	})

	It("returns a snapshot that shares no memory with the live store", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))
		store.RecordScan(domain.ScanRecord{ScanID: "scan-1"})

		snap := store.Snapshot()
		snap.Observations[0].LastClassification.Classification = domain.SuspectedKernelPanic
		snap.LastScan.ScanID = "mutated"

		fresh := store.Snapshot()
		Expect(fresh.Observations[0].LastClassification.Classification).To(Equal(domain.NoTargetFailureVisible))
		Expect(fresh.LastScan.ScanID).To(Equal("scan-1"))
	})

	It("prunes an entry whose namespace was listed but no longer contains it", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))
		store.RecordObservation(classifiedObservation("ns-a", "vmi-2", now))

		store.Prune([]string{"ns-a"}, map[string]struct{}{"ns-a/vmi-2": {}})

		snap := store.Snapshot()
		Expect(snap.Observations).To(HaveLen(1))
		Expect(snap.Observations[0].Name).To(Equal("vmi-2"))
	})

	It("leaves a namespace untouched when it is absent from listedNamespaces", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))

		// A namespace that failed to list this pass contributes no keys to
		// live; pruning it anyway would wrongly evict every entry in it.
		store.Prune([]string{"ns-b"}, map[string]struct{}{})

		Expect(store.Snapshot().Observations).To(HaveLen(1))
	})

	It("Get reports false for a VMI that was never recorded", func() {
		store := dashboard.NewStore(10)
		_, ok := store.Get("ns-a", "vmi-1")
		Expect(ok).To(BeFalse())
	})

	It("Get returns a copy of the recorded entry", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))

		entry, ok := store.Get("ns-a", "vmi-1")
		Expect(ok).To(BeTrue())
		Expect(entry.Namespace).To(Equal("ns-a"))
		Expect(entry.Name).To(Equal("vmi-1"))
	})

	It("SetTriage attaches a triage record to an existing entry", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))

		store.SetTriage("ns-a", "vmi-1", domain.TriageRecord{
			Namespace: "ns-a",
			Name:      "vmi-1",
			Result:    &domain.TriageResult{SuspectedCause: domain.CauseKernelPanic, Confidence: domain.ConfidenceHigh},
		})

		snap := store.Snapshot()
		Expect(snap.Observations).To(HaveLen(1))
		Expect(snap.Observations[0].LastTriage).NotTo(BeNil())
		Expect(snap.Observations[0].LastTriage.Result.SuspectedCause).To(Equal(domain.CauseKernelPanic))
	})

	It("SetTriage is a no-op when the entry does not exist", func() {
		store := dashboard.NewStore(10)
		store.SetTriage("ns-a", "vmi-1", domain.TriageRecord{Namespace: "ns-a", Name: "vmi-1"})

		Expect(store.Snapshot().Observations).To(BeEmpty())
	})

	It("clone deep-copies LastTriage so a snapshot shares no memory with the live store", func() {
		store := dashboard.NewStore(10)
		now := time.Now()
		store.RecordObservation(classifiedObservation("ns-a", "vmi-1", now))
		store.SetTriage("ns-a", "vmi-1", domain.TriageRecord{
			Namespace: "ns-a",
			Name:      "vmi-1",
			Result:    &domain.TriageResult{SuspectedCause: domain.CauseKernelPanic, Confidence: domain.ConfidenceHigh},
		})

		snap := store.Snapshot()
		snap.Observations[0].LastTriage.Result.SuspectedCause = domain.CauseGuestHung

		fresh := store.Snapshot()
		Expect(fresh.Observations[0].LastTriage.Result.SuspectedCause).To(Equal(domain.CauseKernelPanic))
	})

	It("does not panic under concurrent reads and writes", func() {
		store := dashboard.NewStore(50)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					store.RecordObservation(classifiedObservation("ns-a", string(rune('a'+i%26)), time.Now()))
					store.RecordScan(domain.ScanRecord{ScanID: "scan"})
					// Read through the snapshot's pointers, not just its
					// length: an aliased Observation only races when a
					// reader actually dereferences it.
					for _, entry := range store.Snapshot().Observations {
						if entry.LastClassification != nil {
							_ = entry.LastClassification.Classification
						}
					}
				}
			}(i)
		}
		wg.Wait()
	})
})
