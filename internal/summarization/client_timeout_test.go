package summarization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/iris/core"

	"github.com/petal-labs/cortex/internal/llm"
)

// blockingChatProvider hangs on every Chat call until its context is
// canceled, so tests can assert timeout wiring: with a configured timeout
// the call must fail fast with DeadlineExceeded instead of blocking.
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

func newBlockingTestClient(timeout time.Duration) *Client {
	return &Client{
		client:  llm.NewClient(&blockingChatProvider{}),
		model:   "test-model",
		timeout: timeout,
	}
}

// TestCompleteTimeoutFires verifies the configured summarization.timeout
// bounds a hung provider call: without the explicit opt-in the call would
// inherit the SDK's implicit default silently.
func TestCompleteTimeoutFires(t *testing.T) {
	c := newBlockingTestClient(20 * time.Millisecond)

	start := time.Now()
	_, err := c.Complete(context.Background(), "system", "user")
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

// TestCompleteCallerDeadlineWins verifies a caller-supplied deadline is
// never overridden by the configured timeout.
func TestCompleteCallerDeadlineWins(t *testing.T) {
	c := newBlockingTestClient(1 * time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Complete(ctx, "system", "user")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected caller-deadline error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("caller deadline did not win: took %v", elapsed)
	}
}

// TestCompleteTimeoutDisabled verifies timeout <= 0 imposes no deadline of
// its own; only the caller's (20ms here) applies.
func TestCompleteTimeoutDisabled(t *testing.T) {
	c := newBlockingTestClient(0)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Complete(ctx, "system", "user")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected caller-deadline error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected caller deadline only, took %v", elapsed)
	}
}
