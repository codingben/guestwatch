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
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	"github.com/codingben/guestwatch/internal/domain"
	"github.com/codingben/guestwatch/internal/model"
)

// Triager runs a bounded, tool-calling investigation of one VM.
type Triager interface {
	Triage(ctx context.Context, req domain.TriageRequest, tools model.ToolRunner) (domain.TriageResult, domain.Usage, error)
}

var errTriageBusy = errors.New("dashboard: triage concurrency limit reached")

type Server struct {
	addr           string
	staticDir      string
	store          *Store
	logger         *slog.Logger
	triageEnabled  bool
	triage         Triager
	tools          model.ToolRunner
	triageDeadline time.Duration
	triageSem      chan struct{}
	inFlight       singleflight.Group
	triageCancel   map[string]context.CancelFunc
	triageCancelMu sync.Mutex
}

func NewServer(addr, staticDir string, store *Store, logger *slog.Logger) *Server {
	return &Server{addr: addr, staticDir: staticDir, store: store, logger: logger}
}

func (s *Server) EnableTriage(triage Triager, tools model.ToolRunner, deadline time.Duration, concurrency int) {
	s.triageEnabled = true
	s.triage = triage
	s.tools = tools
	s.triageDeadline = deadline
	s.triageSem = make(chan struct{}, concurrency)
	s.triageCancel = make(map[string]context.CancelFunc)
}

func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.GET("/api/observations", s.handleObservations)
	if s.triageEnabled {
		router.POST("/api/vms/:namespace/:name/triage", s.handleTriage)
		router.DELETE("/api/vms/:namespace/:name/triage", s.handleCancelTriage)
	}
	router.NoMethod(func(c *gin.Context) {
		switch {
		case c.Request.URL.Path == "/api/observations":
			c.Header("Allow", http.MethodGet)
		case s.triageEnabled && isTriagePath(c.Request.URL.Path):
			c.Header("Allow", http.MethodPost+", "+http.MethodDelete)
		}
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

func isTriagePath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 5 && parts[0] == "api" && parts[1] == "vms" && parts[4] == "triage"
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
	snap := s.store.Snapshot()
	snap.TriageEnabled = s.triageEnabled

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(snap); err != nil {
		s.logger.Error("dashboard: encode observations", "error", err.Error())
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	c.Data(http.StatusOK, "application/json", buf.Bytes())
}

func (s *Server) handleTriage(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")

	entry, ok := s.store.Get(namespace, name)
	if !ok {
		c.String(http.StatusNotFound, "no such VirtualMachineInstance is being observed")
		return
	}

	key := domain.VMIKey(namespace, name)
	investigateCtx, cancel := context.WithCancel(context.WithoutCancel(c.Request.Context()))
	defer cancel()

	v, err, _ := s.inFlight.Do(key, func() (any, error) {
		select {
		case s.triageSem <- struct{}{}:
		default:
			return nil, errTriageBusy
		}
		defer func() { <-s.triageSem }()

		s.triageCancelMu.Lock()
		s.triageCancel[key] = cancel
		s.triageCancelMu.Unlock()

		defer func() {
			s.triageCancelMu.Lock()
			delete(s.triageCancel, key)
			s.triageCancelMu.Unlock()
		}()

		return s.runTriage(investigateCtx, entry)
	})

	if errors.Is(err, errTriageBusy) {
		c.Header("Retry-After", "5")
		c.String(http.StatusTooManyRequests, "too many triage investigations in flight; try again shortly")
		return
	}
	if err != nil {
		s.logger.Error("dashboard: triage", "namespace", namespace, "name", name, "error", err.Error())
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	c.JSON(http.StatusOK, v.(domain.TriageRecord))
}

func (s *Server) handleCancelTriage(c *gin.Context) {
	key := domain.VMIKey(c.Param("namespace"), c.Param("name"))

	s.triageCancelMu.Lock()
	cancel, ok := s.triageCancel[key]
	s.triageCancelMu.Unlock()

	if !ok {
		c.String(http.StatusNotFound, "no investigation is currently running for this VM")
		return
	}
	cancel()
	c.Status(http.StatusAccepted)
}

func (s *Server) runTriage(ctx context.Context, entry Entry) (domain.TriageRecord, error) {
	target := domain.Target{Namespace: entry.Namespace, Name: entry.Name, Node: entry.Node, UID: entry.UID}

	req := domain.TriageRequest{Target: target}
	if cur := latestObservation(entry); cur != nil {
		req.Classification = cur.Classification
		req.ReasonCode = cur.ReasonCode
		req.ClassificationTime = cur.ObservedAt
	}

	triageCtx, cancel := context.WithTimeout(ctx, s.triageDeadline)
	defer cancel()

	start := time.Now()
	result, usage, triageErr := s.triage.Triage(triageCtx, req, s.tools)

	rec := domain.TriageRecord{
		Namespace:   entry.Namespace,
		Name:        entry.Name,
		UID:         entry.UID,
		RequestedAt: start,
		DurationMS:  time.Since(start).Milliseconds(),
		Usage:       usage,
	}
	if triageErr != nil {
		rec.ErrorCode = triageErrorCode(triageErr)
	} else {
		rec.Result = &result
	}

	if rec.ErrorCode != domain.ErrCancelled {
		s.store.SetTriage(entry.Namespace, entry.Name, rec)
	}

	s.logTriageComplete(rec)
	return rec, nil
}

func latestObservation(entry Entry) *domain.Observation {
	switch {
	case entry.LastClassification != nil && entry.LastFailure != nil:
		if entry.LastFailure.ObservedAt.After(entry.LastClassification.ObservedAt) {
			return entry.LastFailure
		}
		return entry.LastClassification
	case entry.LastClassification != nil:
		return entry.LastClassification
	case entry.LastFailure != nil:
		return entry.LastFailure
	default:
		return nil
	}
}

func triageErrorCode(err error) domain.ErrorCode {
	var te *domain.TargetError
	if errors.As(err, &te) {
		return te.Code
	}

	switch {
	case errors.Is(err, context.Canceled):
		return domain.ErrCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return domain.ErrTimeout
	default:
		return domain.ErrUnavailable
	}
}

func (s *Server) logTriageComplete(rec domain.TriageRecord) {
	attrs := []any{
		"namespace", rec.Namespace,
		"name", rec.Name,
		"duration_ms", rec.DurationMS,
		"input_tokens", rec.Usage.InputTokens,
		"output_tokens", rec.Usage.OutputTokens,
		"reasoning_tokens", rec.Usage.ReasoningTokens,
	}
	if rec.Result != nil {
		attrs = append(attrs,
			"suspected_cause", rec.Result.SuspectedCause,
			"confidence", rec.Result.Confidence,
			"tool_calls", rec.Result.ToolCallCount,
			"tools_used", rec.Result.ToolsUsed,
		)
	} else {
		attrs = append(attrs, "error_code", rec.ErrorCode)
	}
	s.logger.Info("triage_complete", attrs...)
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
