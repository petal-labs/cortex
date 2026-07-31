package embedding

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/iris/core"
)

// fakeEmbeddingProvider is a test double for core.EmbeddingProvider. When block
// is true it hangs until the context is cancelled, simulating a stuck provider
// call. It records the deadline (if any) of the context it received so tests can
// assert on the timeout wiring.
type fakeEmbeddingProvider struct {
	block       bool
	hasDeadline bool
	deadline    time.Time
}

func (f *fakeEmbeddingProvider) CreateEmbeddings(ctx context.Context, req *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	f.deadline, f.hasDeadline = ctx.Deadline()

	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	vectors := make([]core.EmbeddingVector, len(req.Input))
	for i := range vectors {
		vectors[i] = core.EmbeddingVector{Index: i, Vector: []float32{0}}
	}
	return &core.EmbeddingResponse{Vectors: vectors}, nil
}

// TestIrisClientEmbedTimeoutFires verifies that a hung provider call fails fast
// with an error that wraps both ErrProviderFailed and context.DeadlineExceeded,
// rather than blocking until the caller cancels.
func TestIrisClientEmbedTimeoutFires(t *testing.T) {
	fake := &fakeEmbeddingProvider{block: true}
	c := &IrisClient{provider: fake, dimensions: 1, batchSize: 10, timeout: 20 * time.Millisecond}

	start := time.Now()
	_, err := c.Embed(context.Background(), "hello")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected error to wrap context.DeadlineExceeded, got %v", err)
	}
	if !errors.Is(err, ErrProviderFailed) {
		t.Errorf("expected error to wrap ErrProviderFailed, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected fast failure, took %v", elapsed)
	}
	if !fake.hasDeadline {
		t.Error("expected the provider to receive an imposed deadline")
	}
}

// TestIrisClientEmbedTimeoutDisabled verifies that a zero timeout leaves calls
// unbounded: no deadline is imposed when the caller supplies none.
func TestIrisClientEmbedTimeoutDisabled(t *testing.T) {
	fake := &fakeEmbeddingProvider{block: false}
	c := &IrisClient{provider: fake, dimensions: 1, batchSize: 10, timeout: 0}

	if _, err := c.Embed(context.Background(), "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.hasDeadline {
		t.Error("expected no deadline when timeout is disabled and caller has none")
	}
}

// TestIrisClientEmbedRespectsCallerDeadline verifies that a caller-supplied
// deadline is never overridden by the client's own timeout.
func TestIrisClientEmbedRespectsCallerDeadline(t *testing.T) {
	fake := &fakeEmbeddingProvider{block: false}
	c := &IrisClient{provider: fake, dimensions: 1, batchSize: 10, timeout: time.Hour}

	callerDeadline := time.Now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()

	if _, err := c.Embed(ctx, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.hasDeadline {
		t.Fatal("expected caller deadline to be present")
	}
	// The provider must see the caller's deadline, not now+timeout (~1h out).
	if diff := fake.deadline.Sub(callerDeadline); diff < -time.Second || diff > time.Second {
		t.Errorf("expected caller deadline preserved (~%v), provider saw %v", callerDeadline, fake.deadline)
	}
}
