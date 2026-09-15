package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type Server struct {
	addr   string
	store  *Store
	logger *slog.Logger
}

func NewServer(addr string, store *Store, logger *slog.Logger) *Server {
	return &Server{addr: addr, store: store, logger: logger}
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
	router.NoRoute(s.handleNotFound)
	return router
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

func (s *Server) handleNotFound(c *gin.Context) {
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
