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
}

// NewMetricsServer creates a new metrics server on the specified port.
func NewMetricsServer(port int) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	return &MetricsServer{
		port: port,
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
	}
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
