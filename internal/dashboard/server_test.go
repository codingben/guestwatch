package dashboard_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/codingben/kubevirt-ai-agent/internal/dashboard"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var _ = Describe("Server", func() {
	It("serves an empty snapshot as a raw empty array, not null", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", store, discardLogger())
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

	It("returns 404 for an unknown path", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/nope")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("returns 405 for a non-GET method on the API path", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer(":0", store, discardLogger())
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/observations", "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
	})

	It("returns a bind error synchronously instead of blocking", func() {
		store := dashboard.NewStore(10)
		srv := dashboard.NewServer("not-a-valid-address", store, discardLogger())

		// Run binds before it does anything else, so an address that
		// cannot be resolved must fail here rather than block until ctx
		// is cancelled.
		err := srv.Run(context.Background())
		Expect(err).To(HaveOccurred())
	})
})
