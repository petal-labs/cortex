package observability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// MetricsServer serves Prometheus metrics over HTTP.
type MetricsServer struct {
	server *http.Server
	port   int

	// readyCheck verifies dependencies are reachable (e.g. the storage
	// backend's Health). nil means always ready.
	readyCheck func(context.Context) error

	// probeTimeout bounds each /ready check so a hung dependency cannot
	// stack up probe handlers.
	probeTimeout time.Duration
}

// ReadyCheck reports whether the service's dependencies are reachable and
// it can serve traffic. The storage backend's Health method satisfies this.
type ReadyCheck func(context.Context) error

// defaultProbeTimeout bounds a single readiness probe.
const defaultProbeTimeout = 2 * time.Second

// NewMetricsServer creates a new metrics server on the specified port.
// readyCheck may be nil, in which case /ready always reports ready.
func NewMetricsServer(port int, readyCheck ReadyCheck) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// /health is liveness only: it proves the process is up and the HTTP
	// server is accepting connections. It intentionally checks nothing
	// else, so a dead database does not get the pod killed.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	s := &MetricsServer{
		port: port,
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		readyCheck:   readyCheck,
		probeTimeout: defaultProbeTimeout,
	}

	// /ready is readiness: it verifies the storage backend is reachable so
	// orchestrators can distinguish "process up" from "ready to serve" and
	// hold traffic until dependencies are available. External LLM/embedding
	// providers are deliberately not probed — orchestrators poll
	// frequently and probes would burn provider quota; provider failures
	// are already surfaced per-request with retry and classification.
	mux.HandleFunc("/ready", s.handleReady)

	return s
}

// handleReady answers readiness probes: 200 when the readiness check (if
// any) passes within the probe timeout, 503 otherwise.
func (s *MetricsServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.readyCheck == nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.probeTimeout)
	defer cancel()

	if err := s.readyCheck(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "not ready: %v\n", err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ready"))
}

// Start starts the metrics server in the background.
// Returns immediately after starting.
func (s *MetricsServer) Start() {
	go func() {
		if DefaultLogger != nil {
			DefaultLogger.Logger.Info("starting metrics server",
				zap.Int("port", s.port),
				zap.String("endpoint", "/metrics"),
			)
		}

		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			if DefaultLogger != nil {
				DefaultLogger.Logger.Error("metrics server error", zap.Error(err))
			}
		}
	}()
}

// Shutdown gracefully shuts down the metrics server.
func (s *MetricsServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Port returns the port the metrics server is listening on.
func (s *MetricsServer) Port() int {
	return s.port
}
