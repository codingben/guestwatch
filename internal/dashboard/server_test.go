package dashboard_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/codingben/kubevirt-ai-agent/internal/dashboard"
	"github.com/codingben/kubevirt-ai-agent/internal/domain"
	"github.com/codingben/kubevirt-ai-agent/internal/model"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeTriager is a dashboard.Triager whose behavior each test controls.
type fakeTriager struct {
	fn    func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error)
	calls int32
}

func (f *fakeTriager) Triage(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.fn(ctx, req, tools)
}

// fakeTools is a no-op model.ToolRunner; the triage endpoint itself never
// inspects it, only passes it through to the Triager.
type fakeTools struct{}

func (fakeTools) Screenshot(ctx context.Context, target domain.Target) (domain.Screenshot, error) {
	return domain.Screenshot{}, nil
}
func (fakeTools) ConsoleLog(ctx context.Context, target domain.Target, tailLines int, previous bool) (string, error) {
	return "", nil
}
func (fakeTools) ConsoleCapture(ctx context.Context, target domain.Target, durationSeconds int) (string, error) {
	return "", nil
}

func newTriageServer(store *dashboard.Store, triager dashboard.Triager, concurrency int) *dashboard.Server {
	srv := dashboard.NewServer(":0", "", store, discardLogger())
	srv.EnableTriage(triager, fakeTools{}, 5*time.Second, concurrency)
	return srv
}

func classifiedEntryObservation(namespace, name string) domain.Observation {
	return domain.Observation{
		Namespace:      namespace,
		Name:           name,
		Node:           "node-1",
		ScanID:         "scan-1",
		ObservedAt:     time.Now(),
		Outcome:        domain.OutcomeClassified,
		Classification: domain.SuspectedKernelPanic,
		ReasonCode:     domain.KernelPanicVisible,
	}
}

var _ = Describe("Server", func() {
	It("serves an empty snapshot as a raw empty array, not null", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", "", store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/api/observations")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring(`"observations":[]`))

		var snap dashboard.Snapshot
		Expect(json.Unmarshal(body, &snap)).To(Succeed())
	})

	It("returns 404 for an unknown path when no static dir is configured", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", "", store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/nope")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("returns 405 for a non-GET method on the API path", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", "", store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/observations", "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
	})

	It("returns a bind error synchronously instead of blocking", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer("not-a-valid-address", "", store, discardLogger())

		// Run binds before it does anything else, so an address that
		// cannot be resolved must fail here rather than block until ctx
		// is cancelled.
		err := srv.Run(context.Background())
		Expect(err).To(HaveOccurred())
	})

	It("serves the built frontend from staticDir when configured", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>kubevirt-ai-agent</h1>"), 0o644)).To(Succeed())
		assetsDir := filepath.Join(dir, "assets")
		Expect(os.Mkdir(assetsDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(assetsDir, "app.js"), []byte("console.log('ui')"), 0o644)).To(Succeed())

		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", dir, store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		root, err := http.Get(ts.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		defer root.Body.Close()
		Expect(root.StatusCode).To(Equal(http.StatusOK))
		body, err := io.ReadAll(root.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("kubevirt-ai-agent"))

		asset, err := http.Get(ts.URL + "/assets/app.js")
		Expect(err).NotTo(HaveOccurred())
		defer asset.Body.Close()
		Expect(asset.StatusCode).To(Equal(http.StatusOK))

		missing, err := http.Get(ts.URL + "/does-not-exist.js")
		Expect(err).NotTo(HaveOccurred())
		defer missing.Body.Close()
		Expect(missing.StatusCode).To(Equal(http.StatusNotFound))

		// The API keeps working unaffected by static serving.
		api, err := http.Get(ts.URL + "/api/observations")
		Expect(err).NotTo(HaveOccurred())
		defer api.Body.Close()
		Expect(api.StatusCode).To(Equal(http.StatusOK))
	})

	It("404s every UI route when staticDir does not exist, without affecting the API", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", "/nonexistent-kubevirt-ai-agent-ui-dir", store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))

		api, err := http.Get(ts.URL + "/api/observations")
		Expect(err).NotTo(HaveOccurred())
		defer api.Body.Close()
		Expect(api.StatusCode).To(Equal(http.StatusOK))
	})

	It("404s a static subdirectory without index.html instead of listing its contents", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>kubevirt-ai-agent</h1>"), 0o644)).To(Succeed())
		assetsDir := filepath.Join(dir, "assets")
		Expect(os.Mkdir(assetsDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(assetsDir, "app-abc123.js"), []byte("x"), 0o644)).To(Succeed())

		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", dir, store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		for _, p := range []string{"/assets/", "/assets"} {
			resp, err := http.Get(ts.URL + p)
			Expect(err).NotTo(HaveOccurred())
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			Expect(readErr).NotTo(HaveOccurred())

			Expect(resp.StatusCode).To(Equal(http.StatusNotFound), "path %s", p)
			Expect(string(body)).NotTo(ContainSubstring("app-abc123.js"), "path %s leaked a directory listing", p)
		}
	})

	It("404s an unknown /api/ path directly instead of falling through to the static handler", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>kubevirt-ai-agent</h1>"), 0o644)).To(Succeed())

		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", dir, store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/api/bogus")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		// Not the index.html the static handler would have served for any
		// other unmatched path.
		Expect(string(body)).NotTo(ContainSubstring("kubevirt-ai-agent"))
	})

	It("does not register the triage route, and reports it disabled, when EnableTriage was never called", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", "", store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))

		obs, err := http.Get(ts.URL + "/api/observations")
		Expect(err).NotTo(HaveOccurred())
		defer obs.Body.Close()
		var snap dashboard.Snapshot
		Expect(json.NewDecoder(obs.Body).Decode(&snap)).To(Succeed())
		Expect(snap.TriageEnabled).To(BeFalse())
	})

	It("reports triage enabled on /api/observations once EnableTriage was called", func() {
		store := dashboard.NewStore(10)
		srv := newTriageServer(store, &fakeTriager{}, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/api/observations")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		var snap dashboard.Snapshot
		Expect(json.NewDecoder(resp.Body).Decode(&snap)).To(Succeed())
		Expect(snap.TriageEnabled).To(BeTrue())
	})

	It("404s a triage request for a VMI the store has never observed", func() {
		store := dashboard.NewStore(10)
		triager := &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			return domain.TriageResult{}, domain.Usage{}, nil
		}}
		srv := newTriageServer(store, triager, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		Expect(atomic.LoadInt32(&triager.calls)).To(Equal(int32(0)))
	})

	It("runs an investigation and records it on the store for a VMI that has been observed", func() {
		store := dashboard.NewStore(10)
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-1"))

		var capturedReq domain.TriageRequest
		triager := &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			capturedReq = req
			return domain.TriageResult{SuspectedCause: domain.CauseKernelPanic, Confidence: domain.ConfidenceHigh}, domain.Usage{InputTokens: 10}, nil
		}}
		srv := newTriageServer(store, triager, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var rec domain.TriageRecord
		Expect(json.NewDecoder(resp.Body).Decode(&rec)).To(Succeed())
		Expect(rec.Result).NotTo(BeNil())
		Expect(rec.Result.SuspectedCause).To(Equal(domain.CauseKernelPanic))

		// The prior classification is passed through to the Triager.
		Expect(capturedReq.Classification).To(Equal(domain.SuspectedKernelPanic))
		Expect(capturedReq.Target.Namespace).To(Equal("ns-a"))
		Expect(capturedReq.Target.Name).To(Equal("vmi-1"))

		// And is attached back onto the store's entry.
		entry, ok := store.Get("ns-a", "vmi-1")
		Expect(ok).To(BeTrue())
		Expect(entry.LastTriage).NotTo(BeNil())
		Expect(entry.LastTriage.Result.SuspectedCause).To(Equal(domain.CauseKernelPanic))
	})

	It("records a bounded error code, not a raw error, when the Triager fails", func() {
		store := dashboard.NewStore(10)
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-1"))

		triager := &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			return domain.TriageResult{}, domain.Usage{}, &domain.TargetError{Code: domain.ErrTimeout, Msg: "model: triage request: context deadline exceeded"}
		}}
		srv := newTriageServer(store, triager, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var rec domain.TriageRecord
		Expect(json.NewDecoder(resp.Body).Decode(&rec)).To(Succeed())
		Expect(rec.Result).To(BeNil())
		Expect(rec.ErrorCode).To(Equal(domain.ErrTimeout))
	})

	It("returns 429 once the triage concurrency limit is reached", func() {
		store := dashboard.NewStore(10)
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-1"))
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-2"))

		release := make(chan struct{})
		started := make(chan struct{}, 2)
		triager := &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			started <- struct{}{}
			<-release
			return domain.TriageResult{SuspectedCause: domain.CauseIndeterminate, Confidence: domain.ConfidenceLow}, domain.Usage{}, nil
		}}
		srv := newTriageServer(store, triager, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
		}()

		Eventually(started).Should(Receive())

		resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-2/triage", "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusTooManyRequests))
		Expect(resp.Header.Get("Retry-After")).NotTo(BeEmpty())

		close(release)
		wg.Wait()
	})

	It("coalesces concurrent requests for the same VMI into a single investigation", func() {
		store := dashboard.NewStore(10)
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-1"))

		release := make(chan struct{})
		triager := &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			<-release
			return domain.TriageResult{SuspectedCause: domain.CauseIndeterminate, Confidence: domain.ConfidenceLow}, domain.Usage{}, nil
		}}
		srv := newTriageServer(store, triager, 2)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		var wg sync.WaitGroup
		statuses := make([]int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
				Expect(err).NotTo(HaveOccurred())
				statuses[i] = resp.StatusCode
				resp.Body.Close()
			}(i)
		}

		time.Sleep(50 * time.Millisecond) // let both requests reach the singleflight group
		close(release)
		wg.Wait()

		Expect(statuses).To(ConsistOf(http.StatusOK, http.StatusOK))
		Expect(atomic.LoadInt32(&triager.calls)).To(Equal(int32(1)))
	})

	It("returns 405 with Allow: POST, DELETE for a GET on the triage route", func() {
		store := dashboard.NewStore(10)
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-1"))
		srv := newTriageServer(store, &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			return domain.TriageResult{}, domain.Usage{}, nil
		}}, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/api/vms/ns-a/vmi-1/triage")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		Expect(resp.Header.Get("Allow")).To(Equal("POST, DELETE"))
	})

	It("404s a DELETE on the triage route when no investigation is running", func() {
		store := dashboard.NewStore(10)
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-1"))
		srv := newTriageServer(store, &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			return domain.TriageResult{}, domain.Usage{}, nil
		}}, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/vms/ns-a/vmi-1/triage", nil)
		Expect(err).NotTo(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("cancels a running investigation via DELETE, and does not persist the cancelled result", func() {
		store := dashboard.NewStore(10)
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-1"))

		started := make(chan struct{})
		triager := &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			close(started)
			<-ctx.Done()
			return domain.TriageResult{}, domain.Usage{}, ctx.Err()
		}}
		srv := newTriageServer(store, triager, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		var wg sync.WaitGroup
		wg.Add(1)
		var postStatus int
		var rec domain.TriageRecord
		go func() {
			defer wg.Done()
			resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			postStatus = resp.StatusCode
			Expect(json.NewDecoder(resp.Body).Decode(&rec)).To(Succeed())
		}()

		Eventually(started).Should(BeClosed())

		req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/vms/ns-a/vmi-1/triage", nil)
		Expect(err).NotTo(HaveOccurred())
		delResp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer delResp.Body.Close()
		Expect(delResp.StatusCode).To(Equal(http.StatusAccepted))

		wg.Wait()
		Expect(postStatus).To(Equal(http.StatusOK))
		Expect(rec.ErrorCode).To(Equal(domain.ErrCancelled))

		// Not persisted: the store's entry has no LastTriage from this
		// cancelled attempt.
		entry, ok := store.Get("ns-a", "vmi-1")
		Expect(ok).To(BeTrue())
		Expect(entry.LastTriage).To(BeNil())
	})

	It("does not overwrite a previously-successful result when a later investigation is cancelled", func() {
		store := dashboard.NewStore(10)
		store.RecordObservation(classifiedEntryObservation("ns-a", "vmi-1"))

		var n int32
		started := make(chan struct{})
		triager := &fakeTriager{fn: func(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error) {
			if atomic.AddInt32(&n, 1) == 1 {
				return domain.TriageResult{SuspectedCause: domain.CauseKernelPanic, Confidence: domain.ConfidenceHigh}, domain.Usage{}, nil
			}
			close(started)
			<-ctx.Done()
			return domain.TriageResult{}, domain.Usage{}, ctx.Err()
		}}
		srv := newTriageServer(store, triager, 1)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		resp.Body.Close()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(ts.URL+"/api/vms/ns-a/vmi-1/triage", "application/json", nil)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
		}()

		Eventually(started).Should(BeClosed())
		req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/vms/ns-a/vmi-1/triage", nil)
		Expect(err).NotTo(HaveOccurred())
		delResp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		delResp.Body.Close()
		wg.Wait()

		entry, ok := store.Get("ns-a", "vmi-1")
		Expect(ok).To(BeTrue())
		Expect(entry.LastTriage).NotTo(BeNil())
		Expect(entry.LastTriage.Result).NotTo(BeNil())
		Expect(entry.LastTriage.Result.SuspectedCause).To(Equal(domain.CauseKernelPanic))
	})
})
