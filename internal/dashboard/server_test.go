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
		Expect(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>guestwatch</h1>"), 0o644)).To(Succeed())
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
		Expect(string(body)).To(ContainSubstring("guestwatch"))

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
		srv := dashboard.NewServer(":0", "/nonexistent-guestwatch-ui-dir", store, discardLogger())
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
		Expect(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>guestwatch</h1>"), 0o644)).To(Succeed())
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
		Expect(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>guestwatch</h1>"), 0o644)).To(Succeed())

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
		Expect(string(body)).NotTo(ContainSubstring("guestwatch"))
	})
})
