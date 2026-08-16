package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serveReady issues a request against the server's handler and returns the
// response recorder, avoiding port binding entirely.
func serve(t *testing.T, s *MetricsServer, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	return rec
}

func TestHealthEndpoint(t *testing.T) {
	// Liveness must stay green even when dependencies are down — that is
	// the liveness/readiness split; a failing dependency must not get the
	// pod killed.
	s := NewMetricsServer(0, func(context.Context) error {
		return errors.New("backend down")
	})

	rec := serve(t, s, "/health")
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from /health even with failing deps, got %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Errorf("expected body 'ok', got %q", rec.Body.String())
	}
}

func TestReadyEndpoint(t *testing.T) {
	t.Run("nil check reports ready", func(t *testing.T) {
		s := NewMetricsServer(0, nil)
		rec := serve(t, s, "/ready")
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
		if strings.TrimSpace(rec.Body.String()) != "ready" {
			t.Errorf("expected body 'ready', got %q", rec.Body.String())
		}
	})

	t.Run("healthy dependency reports ready", func(t *testing.T) {
		s := NewMetricsServer(0, func(context.Context) error { return nil })
		rec := serve(t, s, "/ready")
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("failing dependency reports not ready with reason", func(t *testing.T) {
		s := NewMetricsServer(0, func(context.Context) error {
			return errors.New("connection refused")
		})
		rec := serve(t, s, "/ready")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "not ready") || !strings.Contains(body, "connection refused") {
			t.Errorf("expected failure reason in body, got %q", body)
		}
	})

	t.Run("hung dependency times out at probe timeout", func(t *testing.T) {
		s := NewMetricsServer(0, func(ctx context.Context) error {
			<-ctx.Done() // hang until the probe deadline kills us
			return ctx.Err()
		})
		s.probeTimeout = 20 * time.Millisecond

		start := time.Now()
		rec := serve(t, s, "/ready")
		elapsed := time.Since(start)

		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503 after probe timeout, got %d", rec.Code)
		}
		if elapsed > 2*time.Second {
			t.Errorf("expected bounded probe, took %v", elapsed)
		}
	})

	t.Run("check receives a deadline", func(t *testing.T) {
		gotDeadline := make(chan bool, 1)
		s := NewMetricsServer(0, func(ctx context.Context) error {
			_, ok := ctx.Deadline()
			gotDeadline <- ok
			return nil
		})
		rec := serve(t, s, "/ready")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		select {
		case ok := <-gotDeadline:
			if !ok {
				t.Error("expected the ready check to run under a deadline")
			}
		default:
			t.Fatal("ready check was not invoked")
		}
	})
}

// TestMetricsStillServed verifies the existing /metrics route is untouched
// by the readiness addition.
func TestMetricsStillServed(t *testing.T) {
	s := NewMetricsServer(0, nil)
	rec := serve(t, s, "/metrics")
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from /metrics, got %d", rec.Code)
	}
}
