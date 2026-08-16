package conformance

import (
	"context"
	"testing"

	"github.com/petal-labs/cortex/internal/storage"
)

func testLifecycle(ctx context.Context, t *testing.T, b storage.Backend) {
	t.Run("Health", func(t *testing.T) {
		if err := b.Health(ctx); err != nil {
			t.Errorf("Health failed: %v", err)
		}
	})

	t.Run("MigrateIdempotent", func(t *testing.T) {
		if err := b.Migrate(ctx); err != nil {
			t.Errorf("re-running Migrate should be idempotent, got: %v", err)
		}
	})
}
