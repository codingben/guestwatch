package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type Server struct {
	addr      string
	staticDir string
	store     *Store
	logger    *slog.Logger
}

func NewServer(addr, staticDir string, store *Store, logger *slog.Logger) *Server {
	return &Server{addr: addr, staticDir: staticDir, store: store, logger: logger}
}

func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.GET("/api/observations", s.handleObservations)
	router.NoMethod(func(c *gin.Context) {
		c.Header("Allow", http.MethodGet)
		c.String(http.StatusMethodNotAllowed, "method not allowed")
	})

	var static http.Handler
	if s.staticDir != "" {
		static = newStaticHandler(s.staticDir)
	}

	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			s.handleAPINotFound(c)
			return
		}
		if static == nil {
			s.handleUINotBuilt(c)
			return
		}
		static.ServeHTTP(c.Writer, c.Request)
	})
	return router
}

func newStaticHandler(dir string) http.Handler {
	fsys := os.DirFS(dir)
	fileServer := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}

		if info, err := fs.Stat(fsys, name); err == nil && info.IsDir() {
			if _, err := fs.Stat(fsys, path.Join(name, "index.html")); err != nil {
				http.NotFound(w, r)
				return
			}
		}

		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) handleObservations(c *gin.Context) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(s.store.Snapshot()); err != nil {
		s.logger.Error("dashboard: encode observations", "error", err.Error())
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	c.Data(http.StatusOK, "application/json", buf.Bytes())
}

func (s *Server) handleAPINotFound(c *gin.Context) {
	c.String(http.StatusNotFound, "no such API route")
}

func (s *Server) handleUINotBuilt(c *gin.Context) {
	c.String(http.StatusNotFound, "dashboard UI not built into this image")
}

func (s *Server) Run(ctx context.Context) error {
	httpServer := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := context.AfterFunc(ctx, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("dashboard: shutdown", "error", err.Error())
		}
	})
	defer stop()

	s.logger.Info("dashboard listening", "addr", s.addr)

	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
