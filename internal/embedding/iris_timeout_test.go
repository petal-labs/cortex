package embedding

import (
	"context"
	"errors"
	"strings"
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

// flakyEmbeddingProvider fails the first N calls then succeeds, recording
// the number of attempts made.
type flakyEmbeddingProvider struct {
	failCount int
	attempts  int
	failWith  error
}

func (f *flakyEmbeddingProvider) CreateEmbeddings(ctx context.Context, req *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	f.attempts++
	if f.attempts <= f.failCount {
		return nil, f.failWith
	}
	vectors := make([]core.EmbeddingVector, len(req.Input))
	for i := range vectors {
		vectors[i] = core.EmbeddingVector{Index: i, Vector: []float32{0}}
	}
	return &core.EmbeddingResponse{Vectors: vectors}, nil
}

// TestIrisClientEmbedRetrySucceeds verifies that a transient 503 is retried
// and the call ultimately succeeds.
func TestIrisClientEmbedRetrySucceeds(t *testing.T) {
	fake := &flakyEmbeddingProvider{
		failCount: 2,
		failWith:  &core.ProviderError{Provider: "test", Status: 503, Message: "service unavailable"},
	}
	c := &IrisClient{
		provider:   fake,
		dimensions: 1,
		batchSize:  10,
		timeout:    0,
		retry:      core.NewRetryPolicy(core.RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}),
	}

	emb, err := c.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedding")
	}
	if fake.attempts != 3 {
		t.Errorf("expected 3 attempts (2 failures + 1 success), got %d", fake.attempts)
	}
}

// TestIrisClientEmbedRetryExhausted verifies that after max retries the
// error is propagated.
func TestIrisClientEmbedRetryExhausted(t *testing.T) {
	fake := &flakyEmbeddingProvider{
		failCount: 100, // always fails
		failWith:  &core.ProviderError{Provider: "test", Status: 503, Message: "service unavailable"},
	}
	c := &IrisClient{
		provider:   fake,
		dimensions: 1,
		batchSize:  10,
		timeout:    0,
		retry:      core.NewRetryPolicy(core.RetryConfig{MaxRetries: 2, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}),
	}

	_, err := c.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}
	if !errors.Is(err, ErrProviderFailed) {
		t.Errorf("expected ErrProviderFailed, got %v", err)
	}
	// 1 initial + 2 retries = 3 total attempts
	if fake.attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", fake.attempts)
	}
}

// TestIrisClientEmbedRetrySkipsNonRetryable verifies that non-retryable
// errors (e.g. 400 Bad Request) are not retried.
func TestIrisClientEmbedRetrySkipsNonRetryable(t *testing.T) {
	fake := &flakyEmbeddingProvider{
		failCount: 100,
		failWith:  &core.ProviderError{Provider: "test", Status: 400, Message: "bad request"},
	}
	c := &IrisClient{
		provider:   fake,
		dimensions: 1,
		batchSize:  10,
		timeout:    0,
		retry:      core.NewRetryPolicy(core.RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}),
	}

	_, err := c.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for bad request, got nil")
	}
	if fake.attempts != 1 {
		t.Errorf("expected 1 attempt (no retry for 400), got %d", fake.attempts)
	}
}

// variableVectorProvider returns vectors with per-input lengths drawn from
// the lengths slice (cycled), simulating wrong-model or inconsistent
// embedding responses.
type variableVectorProvider struct {
	lengths []int
}

func (v *variableVectorProvider) CreateEmbeddings(ctx context.Context, req *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	vectors := make([]core.EmbeddingVector, len(req.Input))
	for i := range vectors {
		l := v.lengths[i%len(v.lengths)]
		vec := make([]float32, l)
		for j := range vec {
			vec[j] = float32(j) * 0.01
		}
		vectors[i] = core.EmbeddingVector{Index: i, Vector: vec}
	}
	return &core.EmbeddingResponse{Vectors: vectors}, nil
}

// TestIrisClientEmbedDimensionMismatch verifies that a wrong-model response
// (e.g. 768-dim vectors against a 1536-dim config) fails with a clear error
// instead of passing wrong-width vectors downstream.
func TestIrisClientEmbedDimensionMismatch(t *testing.T) {
	fake := &variableVectorProvider{lengths: []int{2}}
	c := &IrisClient{provider: fake, dimensions: 4, batchSize: 10}

	_, err := c.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected dimension mismatch error, got nil")
	}
	if !errors.Is(err, ErrProviderFailed) {
		t.Errorf("expected ErrProviderFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "has 2 dimensions, expected 4") {
		t.Errorf("expected clear dimension message, got: %v", err)
	}
}

// TestIrisClientEmbedInconsistentDimensions verifies that when dimensions
// are unset, ragged vector output is rejected rather than silently inferred
// from the first vector.
func TestIrisClientEmbedInconsistentDimensions(t *testing.T) {
	fake := &variableVectorProvider{lengths: []int{3, 2}}
	c := &IrisClient{provider: fake, dimensions: 0, batchSize: 10}

	_, err := c.EmbedBatch(context.Background(), []string{"one", "two"})
	if err == nil {
		t.Fatal("expected inconsistent dimensions error, got nil")
	}
	if !errors.Is(err, ErrProviderFailed) {
		t.Errorf("expected ErrProviderFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "inconsistent embedding dimensions") {
		t.Errorf("expected inconsistency message, got: %v", err)
	}
}

// TestIrisClientEmbedConsistentUnsetDimensions verifies that when dimensions
// are unset, a consistent response is accepted and dims are inferred from
// the first vector.
func TestIrisClientEmbedConsistentUnsetDimensions(t *testing.T) {
	fake := &variableVectorProvider{lengths: []int{3}}
	c := &IrisClient{provider: fake, dimensions: 0, batchSize: 10}

	embs, err := c.EmbedBatch(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("expected success with consistent vectors, got: %v", err)
	}
	if len(embs) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embs))
	}
	for i, e := range embs {
		if len(e) != 3 {
			t.Errorf("expected embedding %d to have 3 dimensions, got %d", i, len(e))
		}
	}
}

// shuffledProvider returns vectors in reverse order with correct .Index
// fields, simulating a provider that does not guarantee response order.
type shuffledProvider struct {
	dims int
}

func (s *shuffledProvider) CreateEmbeddings(ctx context.Context, req *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	vectors := make([]core.EmbeddingVector, len(req.Input))
	for i := range vectors {
		// Position i in the response carries the vector for input index
		// len-1-i (reversed), with .Index telling the truth.
		j := len(req.Input) - 1 - i
		vec := make([]float32, s.dims)
		vec[0] = float32(j) // identifiable per input
		vectors[i] = core.EmbeddingVector{Index: j, Vector: vec}
	}
	return &core.EmbeddingResponse{Vectors: vectors}, nil
}

// TestIrisClientEmbedBatchMapsByIndex verifies that an out-of-order response
// is reassembled by each vector's .Index field, not response position.
func TestIrisClientEmbedBatchMapsByIndex(t *testing.T) {
	fake := &shuffledProvider{dims: 2}
	c := &IrisClient{provider: fake, dimensions: 2, batchSize: 10}

	embs, err := c.EmbedBatch(context.Background(), []string{"zero", "one", "two"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embs) != 3 {
		t.Fatalf("expected 3 embeddings, got %d", len(embs))
	}
	for i, e := range embs {
		if len(e) != 2 {
			t.Fatalf("expected embedding %d to have 2 dims, got %d", i, len(e))
		}
		if e[0] != float32(i) {
			t.Errorf("embedding %d attached vector for input %d (position-based mapping bug)", i, int(e[0]))
		}
	}
}

// TestIrisClientEmbedBatchMapsByIndexWithEmptyInputs verifies the index
// mapping still lands correctly when empty inputs shift the original
// positions.
func TestIrisClientEmbedBatchMapsByIndexWithEmptyInputs(t *testing.T) {
	fake := &shuffledProvider{dims: 1}
	c := &IrisClient{provider: fake, dimensions: 1, batchSize: 10}

	// Input 0 is empty; "alpha" is nonEmpty[0] → original position 1,
	// "beta" is nonEmpty[1] → original position 2.
	embs, err := c.EmbedBatch(context.Background(), []string{"", "alpha", "beta"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Zero vector for the empty input.
	if embs[0][0] != 0 {
		t.Errorf("expected zero vector for empty input, got %v", embs[0])
	}
	// shuffledProvider marks each vector with its nonEmpty index.
	if embs[1][0] != 0 {
		t.Errorf("expected 'alpha' to carry nonEmpty index 0, got %v", embs[1][0])
	}
	if embs[2][0] != 1 {
		t.Errorf("expected 'beta' to carry nonEmpty index 1, got %v", embs[2][0])
	}
}

// outOfBoundsProvider returns a vector with an index beyond the input count.
type outOfBoundsProvider struct {
	index int
	dims  int
}

func (o *outOfBoundsProvider) CreateEmbeddings(ctx context.Context, req *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	vec := make([]float32, o.dims)
	return &core.EmbeddingResponse{Vectors: []core.EmbeddingVector{{Index: o.index, Vector: vec}}}, nil
}

// TestIrisClientEmbedBatchIndexOutOfBounds verifies an out-of-range index
// fails loudly instead of panicking or silently misplacing the vector.
func TestIrisClientEmbedBatchIndexOutOfBounds(t *testing.T) {
	fake := &outOfBoundsProvider{index: 5, dims: 2}
	c := &IrisClient{provider: fake, dimensions: 2, batchSize: 10}

	_, err := c.EmbedBatch(context.Background(), []string{"one"})
	if err == nil {
		t.Fatal("expected out-of-range index error, got nil")
	}
	if !errors.Is(err, ErrProviderFailed) {
		t.Errorf("expected ErrProviderFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("expected out-of-range message, got: %v", err)
	}
}

// duplicateIndexProvider returns two vectors that both claim index 0.
type duplicateIndexProvider struct {
	dims int
}

func (d *duplicateIndexProvider) CreateEmbeddings(ctx context.Context, req *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	vec := make([]float32, d.dims)
	return &core.EmbeddingResponse{Vectors: []core.EmbeddingVector{
		{Index: 0, Vector: vec},
		{Index: 0, Vector: vec},
	}}, nil
}

// TestIrisClientEmbedBatchDuplicateIndex verifies duplicate indices fail
// loudly instead of silently overwriting one input's vector with another's.
func TestIrisClientEmbedBatchDuplicateIndex(t *testing.T) {
	fake := &duplicateIndexProvider{dims: 2}
	c := &IrisClient{provider: fake, dimensions: 2, batchSize: 10}

	_, err := c.EmbedBatch(context.Background(), []string{"one", "two"})
	if err == nil {
		t.Fatal("expected duplicate index error, got nil")
	}
	if !errors.Is(err, ErrProviderFailed) {
		t.Errorf("expected ErrProviderFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "duplicates index") {
		t.Errorf("expected duplicate-index message, got: %v", err)
	}
}

// recordingProvider records the input texts of each provider call and
// returns vectors marked with the first byte of each input text, so tests
// can assert both split boundaries and result ordering. Optionally fails
// on the Nth call (1-based) with a non-retryable 400.
type recordingProvider struct {
	dims       int
	calls      [][]string
	failOnCall int
}

func (r *recordingProvider) CreateEmbeddings(ctx context.Context, req *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	texts := make([]string, len(req.Input))
	for i, in := range req.Input {
		texts[i] = in.Text
	}
	r.calls = append(r.calls, texts)

	if r.failOnCall > 0 && len(r.calls) == r.failOnCall {
		return nil, &core.ProviderError{Provider: "test", Status: 400, Message: "boom"}
	}

	vectors := make([]core.EmbeddingVector, len(req.Input))
	for i, in := range req.Input {
		vec := make([]float32, r.dims)
		vec[0] = float32(in.Text[0]) // inputs are non-empty in these tests
		vectors[i] = core.EmbeddingVector{Index: i, Vector: vec}
	}
	return &core.EmbeddingResponse{Vectors: vectors}, nil
}

// TestIrisClientEmbedBatchAutoSplits verifies that inputs exceeding the
// batch size are split into batch-sized provider calls and the results are
// concatenated in input order.
func TestIrisClientEmbedBatchAutoSplits(t *testing.T) {
	fake := &recordingProvider{dims: 2}
	c := &IrisClient{provider: fake, dimensions: 2, batchSize: 2}

	texts := []string{"a", "b", "c", "d", "e"}
	embs, err := c.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("expected auto-splitting to succeed, got: %v", err)
	}

	if len(embs) != 5 {
		t.Fatalf("expected 5 embeddings, got %d", len(embs))
	}
	// Split boundaries: [a b] [c d] [e]
	if len(fake.calls) != 3 {
		t.Fatalf("expected 3 provider calls, got %d", len(fake.calls))
	}
	for i, want := range [][]string{{"a", "b"}, {"c", "d"}, {"e"}} {
		got := fake.calls[i]
		if len(got) != len(want) {
			t.Fatalf("call %d: expected %d inputs, got %d", i, len(want), len(got))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Errorf("call %d input %d: expected %q, got %q", i, j, want[j], got[j])
			}
		}
	}
	// Concatenated in input order.
	for i, text := range texts {
		if embs[i][0] != float32(text[0]) {
			t.Errorf("result %d carries %q's vector, expected %q's", i, string(rune(embs[i][0])), text)
		}
	}
}

// TestIrisClientEmbedBatchAutoSplitFailurePropagates verifies a failing
// sub-batch fails the whole call rather than returning partial results.
func TestIrisClientEmbedBatchAutoSplitFailurePropagates(t *testing.T) {
	fake := &recordingProvider{dims: 2, failOnCall: 2}
	c := &IrisClient{provider: fake, dimensions: 2, batchSize: 2}

	_, err := c.EmbedBatch(context.Background(), []string{"a", "b", "c", "d"})
	if err == nil {
		t.Fatal("expected error when a sub-batch fails, got nil")
	}
	if !errors.Is(err, ErrProviderFailed) {
		t.Errorf("expected ErrProviderFailed, got %v", err)
	}
	if len(fake.calls) != 2 {
		t.Errorf("expected processing to stop at the failed call, got %d calls", len(fake.calls))
	}
}

// TestIrisClientEmbedBatchAutoSplitWithEmptyInputs verifies empty inputs
// get zero vectors at their original positions across sub-batches.
func TestIrisClientEmbedBatchAutoSplitWithEmptyInputs(t *testing.T) {
	fake := &recordingProvider{dims: 1}
	c := &IrisClient{provider: fake, dimensions: 1, batchSize: 2}

	texts := []string{"", "a", "", "b", "c"}
	embs, err := c.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embs) != 5 {
		t.Fatalf("expected 5 embeddings, got %d", len(embs))
	}
	// Zero vectors at the empty positions.
	for _, i := range []int{0, 2} {
		if embs[i][0] != 0 {
			t.Errorf("expected zero vector at position %d, got %v", i, embs[i])
		}
	}
	// Correct vectors at the non-empty positions.
	if embs[1][0] != 'a' || embs[3][0] != 'b' || embs[4][0] != 'c' {
		t.Errorf("misordered results across sub-batches: got %v %v %v",
			string(rune(embs[1][0])), string(rune(embs[3][0])), string(rune(embs[4][0])))
	}
	// Empty inputs are dropped from provider calls: sub-batches split on the
	// original positions ["", "a"] ["", "b"] ["c"] → one non-empty each.
	if len(fake.calls) != 3 {
		t.Errorf("unexpected provider call count: %v", fake.calls)
	}
	for i, call := range fake.calls {
		if len(call) != 1 {
			t.Errorf("call %d: expected 1 non-empty input, got %v", i, call)
		}
	}
}

// TestIrisClientEmbedBatchNoLimit verifies batchSize <= 0 means no limit:
// everything goes in a single provider call.
func TestIrisClientEmbedBatchNoLimit(t *testing.T) {
	fake := &recordingProvider{dims: 1}
	c := &IrisClient{provider: fake, dimensions: 1, batchSize: 0}

	embs, err := c.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embs) != 3 {
		t.Fatalf("expected 3 embeddings, got %d", len(embs))
	}
	if len(fake.calls) != 1 {
		t.Errorf("expected 1 provider call with no batch limit, got %d", len(fake.calls))
	}
}

// TestIrisClientEmptyBatchRequiresDimensions verifies the all-empty-batch
// edge case: with dimensions unset there is no provider response to infer
// width from, so the client rejects instead of returning zero-LENGTH
// vectors that would surface as ragred output downstream.
func TestIrisClientEmptyBatchRequiresDimensions(t *testing.T) {
	fake := &fakeEmbeddingProvider{block: false} // never called for all-empty input
	c := &IrisClient{provider: fake, dimensions: 0, batchSize: 10}

	_, err := c.EmbedBatch(context.Background(), []string{"", ""})
	if err == nil {
		t.Fatal("expected error for all-empty batch with unset dimensions, got nil")
	}
	if !errors.Is(err, ErrProviderFailed) {
		t.Errorf("expected ErrProviderFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), "embedding dimensions are unset") {
		t.Errorf("expected actionable message, got: %v", err)
	}
}

// TestIrisClientEmptyBatchConfiguredDimensions verifies an all-empty batch
// with configured dimensions returns zero vectors of exactly that width.
func TestIrisClientEmptyBatchConfiguredDimensions(t *testing.T) {
	fake := &fakeEmbeddingProvider{block: false}
	c := &IrisClient{provider: fake, dimensions: 384, batchSize: 10}

	embs, err := c.EmbedBatch(context.Background(), []string{"", ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embs) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embs))
	}
	for i, e := range embs {
		if len(e) != 384 {
			t.Errorf("expected embedding %d to have width 384, got %d", i, len(e))
		}
	}
}

// TestIrisClientMixedBatchInferredDimensions verifies the mixed batch with
// unset dimensions still infers width from the provider response for the
// empty positions (the legitimate inference path this guard must not break).
func TestIrisClientMixedBatchInferredDimensions(t *testing.T) {
	fake := &variableVectorProvider{lengths: []int{3}}
	c := &IrisClient{provider: fake, dimensions: 0, batchSize: 10}

	embs, err := c.EmbedBatch(context.Background(), []string{"real", ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embs) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embs))
	}
	if len(embs[0]) != 3 {
		t.Errorf("expected real input to carry 3-dim vector, got %d", len(embs[0]))
	}
	if len(embs[1]) != 3 {
		t.Errorf("expected empty input zero vector to match inferred width 3, got %d", len(embs[1]))
	}
}
