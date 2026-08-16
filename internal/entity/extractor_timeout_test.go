package entity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/iris/core"

	"github.com/petal-labs/cortex/internal/llm"
)

// blockingChatProvider hangs on every Chat call until its context is
// canceled, so tests can assert timeout wiring in the extraction path.
type blockingChatProvider struct{}

func (p *blockingChatProvider) ID() string                 { return "blocking" }
func (p *blockingChatProvider) Models() []core.ModelInfo   { return nil }
func (p *blockingChatProvider) Supports(core.Feature) bool { return false }
func (p *blockingChatProvider) StreamChat(ctx context.Context, _ *core.ChatRequest) (*core.ChatStream, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p *blockingChatProvider) Chat(ctx context.Context, _ *core.ChatRequest) (*core.ChatResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func newBlockingTestExtractor(timeout time.Duration) *Extractor {
	return &Extractor{
		client:  llm.NewClient(&blockingChatProvider{}),
		model:   "test-model",
		timeout: timeout,
	}
}

// TestExtractTimeoutFires verifies the configured entity.extraction_timeout
// bounds a hung provider call: without the explicit opt-in the call would
// inherit the SDK's implicit default silently.
func TestExtractTimeoutFires(t *testing.T) {
	e := newBlockingTestExtractor(20 * time.Millisecond)

	start := time.Now()
	_, err := e.Extract(context.Background(), "John works at Acme Corp.")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected fast failure, took %v", elapsed)
	}
}

// TestExtractCallerDeadlineWins verifies a caller-supplied deadline is
// never overridden by the configured timeout.
func TestExtractCallerDeadlineWins(t *testing.T) {
	e := newBlockingTestExtractor(1 * time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := e.Extract(ctx, "John works at Acme Corp.")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected caller-deadline error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("caller deadline did not win: took %v", elapsed)
	}
}

// TestExtractTimeoutDisabled verifies timeout <= 0 imposes no deadline of
// its own; only the caller's (20ms here) applies.
func TestExtractTimeoutDisabled(t *testing.T) {
	e := newBlockingTestExtractor(0)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := e.Extract(ctx, "John works at Acme Corp.")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected caller-deadline error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected caller deadline only, took %v", elapsed)
	}
}
