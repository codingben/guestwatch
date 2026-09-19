package dashboard

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/codingben/guestwatch/internal/domain"
)

// DefaultMaxObservations bounds the store's memory footprint independent
// of fleet size: past this many distinct VMIs, the least-recently-updated
// entry is evicted to make room for a new one.
const DefaultMaxObservations = 1000

type Entry struct {
	Namespace          string              `json:"namespace"`
	Name               string              `json:"name"`
	Node               string              `json:"node"`
	UID                types.UID           `json:"uid"`
	UpdatedAt          time.Time           `json:"updatedAt"`
	LastClassification *domain.Observation `json:"lastClassification,omitempty"`
	LastFailure        *domain.Observation `json:"lastFailure,omitempty"`

	// Only Server.handleTriage sets this; the scan loop never does.
	LastTriage *domain.TriageRecord `json:"lastTriage,omitempty"`
}

type Snapshot struct {
	LastScan      *domain.ScanRecord `json:"lastScan"`
	Observations  []Entry            `json:"observations"`
	TriageEnabled bool               `json:"triageEnabled"`
}

type Store struct {
	mu       sync.RWMutex
	max      int
	byVMI    map[string]*Entry
	lastScan *domain.ScanRecord
}

func NewStore(max int) *Store {
	if max <= 0 {
		max = DefaultMaxObservations
	}
	return &Store{
		max:   max,
		byVMI: make(map[string]*Entry),
	}
}

func (s *Store) RecordObservation(obs domain.Observation) {
	key := domain.VMIKey(obs.Namespace, obs.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.byVMI[key]
	if !ok {
		if len(s.byVMI) >= s.max {
			s.evictOldestLocked()
		}
		e = &Entry{Namespace: obs.Namespace, Name: obs.Name}
		s.byVMI[key] = e
	}

	e.Node = obs.Node
	e.UID = obs.UID
	e.UpdatedAt = obs.ObservedAt

	switch obs.Outcome {
	case domain.OutcomeClassified:
		e.LastClassification = &obs
	case domain.OutcomeFailed:
		e.LastFailure = &obs
	}
}

func (s *Store) RecordScan(rec domain.ScanRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastScan = &rec
}

func (s *Store) Get(namespace, name string) (Entry, bool) {
	key := domain.VMIKey(namespace, name)

	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.byVMI[key]
	if !ok {
		return Entry{}, false
	}
	return e.clone(), true
}

func (s *Store) SetTriage(namespace, name string, rec domain.TriageRecord) {
	key := domain.VMIKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.byVMI[key]; ok {
		e.LastTriage = &rec
	}
}

func (s *Store) Prune(listedNamespaces []string, live map[string]struct{}) {
	if len(listedNamespaces) == 0 {
		return
	}
	listed := make(map[string]struct{}, len(listedNamespaces))
	for _, ns := range listedNamespaces {
		listed[ns] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for key, e := range s.byVMI {
		if _, ok := listed[e.Namespace]; !ok {
			continue
		}
		if _, ok := live[key]; !ok {
			delete(s.byVMI, key)
		}
	}
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]Entry, 0, len(s.byVMI))
	for _, e := range s.byVMI {
		entries = append(entries, e.clone())
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})

	var lastScan *domain.ScanRecord
	if s.lastScan != nil {
		recCopy := *s.lastScan
		lastScan = &recCopy
	}

	return Snapshot{
		LastScan:     lastScan,
		Observations: entries,
	}
}

func (e *Entry) clone() Entry {
	c := *e
	if e.LastClassification != nil {
		obs := *e.LastClassification
		c.LastClassification = &obs
	}
	if e.LastFailure != nil {
		obs := *e.LastFailure
		c.LastFailure = &obs
	}
	if e.LastTriage != nil {
		rec := *e.LastTriage
		if e.LastTriage.Result != nil {
			result := *e.LastTriage.Result
			rec.Result = &result
		}
		c.LastTriage = &rec
	}
	return c
}

func (s *Store) evictOldestLocked() {
	var oldestKey string
	var oldestAt time.Time
	for k, e := range s.byVMI {
		if oldestKey == "" || e.UpdatedAt.Before(oldestAt) {
			oldestKey, oldestAt = k, e.UpdatedAt
		}
	}
	delete(s.byVMI, oldestKey)
}
