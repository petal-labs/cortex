package llm

import (
	"context"
	"errors"

	"github.com/petal-labs/iris/core"
	"go.uber.org/zap"

	"github.com/petal-labs/cortex/internal/observability"
)

// ErrorKind classifies a provider failure by the operational response it
// demands, derived from the iris typed error taxonomy
// (core.ProviderError + sentinels) rather than message text.
type ErrorKind string

const (
	// ErrorKindUnknown means the error is not a recognized provider error;
	// no classification is possible.
	ErrorKindUnknown ErrorKind = "unknown"

	// ErrorKindPermanent means retrying cannot succeed — a bad API key
	// (401/403), a malformed request (400), or an unparsable response.
	// Stop, alert on configuration, and dead-letter queued work.
	ErrorKindPermanent ErrorKind = "permanent"

	// ErrorKindRateLimited means the provider returned 429: back off before
	// retrying, and alert if sustained.
	ErrorKindRateLimited ErrorKind = "rate_limited"

	// ErrorKindTransient means a server-side (5xx) or network failure:
	// safe to retry with backoff.
	ErrorKindTransient ErrorKind = "transient"
)

// ClassifyError inspects err against the iris taxonomy and returns the
// operational kind plus the typed *core.ProviderError when present (nil for
// non-provider errors). The kind is derived from the sentinel errors (which
// survive %w wrapping), so bare sentinel chains classify too; the typed
// struct carries Status/Code/RequestID for alerting when available.
func ClassifyError(err error) (ErrorKind, *core.ProviderError) {
	if err == nil {
		return ErrorKindUnknown, nil
	}

	var pgErr *core.ProviderError
	errors.As(err, &pgErr)

	switch {
	case errors.Is(err, core.ErrUnauthorized),
		errors.Is(err, core.ErrBadRequest),
		errors.Is(err, core.ErrDecode),
		errors.Is(err, core.ErrNotSupported):
		return ErrorKindPermanent, pgErr
	case errors.Is(err, core.ErrRateLimited):
		return ErrorKindRateLimited, pgErr
	case errors.Is(err, core.ErrServer),
		errors.Is(err, core.ErrNetwork):
		return ErrorKindTransient, pgErr
	default:
		if pgErr != nil {
			// A typed provider error with an unmapped sentinel defaults to
			// transient; a bare unknown error is not recognizably a
			// provider failure at all.
			return ErrorKindTransient, pgErr
		}
		return ErrorKindUnknown, nil
	}
}

// LogProviderError emits a structured log line for a failed provider call,
// including the classification, HTTP status, provider error code, and the
// provider RequestID so operators can escalate with support. No-op for
// non-provider errors (the raw error is still returned to the caller).
func LogProviderError(ctx context.Context, operation string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	kind, pgErr := ClassifyError(err)
	if pgErr == nil {
		observability.Error(ctx, "provider call failed",
			zap.String("operation", operation),
			zap.String("kind", string(ErrorKindUnknown)),
			zap.Error(err))
		return
	}

	observability.Error(ctx, "provider call failed",
		zap.String("operation", operation),
		zap.String("kind", string(kind)),
		zap.String("provider", pgErr.Provider),
		zap.Int("status", pgErr.Status),
		zap.String("code", pgErr.Code),
		zap.String("request_id", pgErr.RequestID),
		zap.Error(err))
}
