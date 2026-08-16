package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/petal-labs/iris/core"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantKind     ErrorKind
		wantProvider bool
	}{
		{
			name:     "nil error",
			err:      nil,
			wantKind: ErrorKindUnknown,
		},
		{
			name:     "non-provider error",
			err:      errors.New("connection refused"),
			wantKind: ErrorKindUnknown,
		},
		{
			name: "401 unauthorized is permanent",
			err: &core.ProviderError{
				Provider: "openai", Status: 401, Code: "invalid_api_key",
				RequestID: "req-1", Err: core.ErrUnauthorized,
			},
			wantKind:     ErrorKindPermanent,
			wantProvider: true,
		},
		{
			name: "403 forbidden is permanent",
			err: &core.ProviderError{
				Provider: "openai", Status: 403, Err: core.ErrUnauthorized,
			},
			wantKind:     ErrorKindPermanent,
			wantProvider: true,
		},
		{
			name: "400 bad request is permanent",
			err: &core.ProviderError{
				Provider: "openai", Status: 400, Err: core.ErrBadRequest,
			},
			wantKind:     ErrorKindPermanent,
			wantProvider: true,
		},
		{
			name: "429 rate limited",
			err: &core.ProviderError{
				Provider: "openai", Status: 429, Code: "rate_limit_exceeded",
				RequestID: "req-2", Err: core.ErrRateLimited,
			},
			wantKind:     ErrorKindRateLimited,
			wantProvider: true,
		},
		{
			name: "500 server error is transient",
			err: &core.ProviderError{
				Provider: "openai", Status: 503, Err: core.ErrServer,
			},
			wantKind:     ErrorKindTransient,
			wantProvider: true,
		},
		{
			name: "network error is transient",
			err: &core.ProviderError{
				Provider: "openai", Err: core.ErrNetwork,
			},
			wantKind:     ErrorKindTransient,
			wantProvider: true,
		},
		{
			name: "decode error is permanent",
			err: &core.ProviderError{
				Provider: "openai", Err: core.ErrDecode,
			},
			wantKind:     ErrorKindPermanent,
			wantProvider: true,
		},
		{
			name: "unmapped sentinel on ProviderError defaults to transient",
			err: &core.ProviderError{
				Provider: "openai", Status: 418, Err: errors.New("teapot"),
			},
			wantKind:     ErrorKindTransient,
			wantProvider: true,
		},
		{
			name:     "bare unauthorized sentinel classifies without typed error",
			err:      fmt.Errorf("extraction failed: %w", core.ErrUnauthorized),
			wantKind: ErrorKindPermanent,
		},
		{
			name:     "bare rate-limited sentinel classifies without typed error",
			err:      core.ErrRateLimited,
			wantKind: ErrorKindRateLimited,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, pgErr := ClassifyError(tc.err)
			if kind != tc.wantKind {
				t.Errorf("expected kind %s, got %s", tc.wantKind, kind)
			}
			if tc.wantProvider && pgErr == nil {
				t.Error("expected non-nil ProviderError")
			}
			if !tc.wantProvider && pgErr != nil {
				t.Errorf("expected nil ProviderError, got %v", pgErr)
			}
		})
	}
}

// TestClassifyErrorSeesThroughWrapping verifies classification works
// through the %w chains Cortex builds at each call site: the embedding
// client, the extractor, and the queue processor all wrap provider errors.
func TestClassifyErrorSeesThroughWrapping(t *testing.T) {
	inner := &core.ProviderError{
		Provider: "openai", Status: 401, Code: "invalid_api_key",
		RequestID: "req-wrap", Err: core.ErrUnauthorized,
	}
	wrapped := fmt.Errorf("extraction completion failed: %w",
		fmt.Errorf("extraction failed: %w", inner))

	kind, pgErr := ClassifyError(wrapped)
	if kind != ErrorKindPermanent {
		t.Errorf("expected permanent through wrapping, got %s", kind)
	}
	if pgErr == nil {
		t.Fatal("expected typed ProviderError through wrapping")
	}
	if pgErr.RequestID != "req-wrap" {
		t.Errorf("expected RequestID req-wrap, got %q", pgErr.RequestID)
	}
	if pgErr.Status != 401 {
		t.Errorf("expected status 401, got %d", pgErr.Status)
	}
}

// TestLogProviderErrorDoesNotPanic exercises both the provider-error and
// non-provider paths. Asserting on log output is not practical without
// injecting a logger; the classification itself is covered above.
func TestLogProviderErrorDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	t.Run("provider error", func(t *testing.T) {
		LogProviderError(ctx, "test", &core.ProviderError{
			Provider: "openai", Status: 429, Err: core.ErrRateLimited,
		})
	})
	t.Run("non-provider error", func(t *testing.T) {
		LogProviderError(ctx, "test", errors.New("boom"))
	})
	t.Run("nil error", func(t *testing.T) {
		LogProviderError(ctx, "test", nil)
	})
}
