package gc

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/pkg/types"
)

func TestCollectorRunVoiceTranscriptCleanup(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	store := sqlite.NewWithDB(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	for _, m := range []*types.Message{
		{
			Namespace: "voice-gc",
			ThreadID:  "old-voice-thread",
			Role:      "user",
			Content:   "old voice transcript",
			Metadata: map[string]string{
				types.VoiceMetaSessionID: "sess-old",
				types.VoiceMetaFinal:     "true",
			},
			CreatedAt: now.Add(-10 * 24 * time.Hour),
		},
		{
			Namespace: "voice-gc",
			ThreadID:  "fresh-voice-thread",
			Role:      "user",
			Content:   "fresh voice transcript",
			Metadata: map[string]string{
				types.VoiceMetaSessionID: "sess-fresh",
				types.VoiceMetaFinal:     "true",
			},
			CreatedAt: now,
		},
		{
			Namespace: "voice-gc",
			ThreadID:  "old-text-thread",
			Role:      "user",
			Content:   "old text message",
			CreatedAt: now.Add(-10 * 24 * time.Hour),
		},
	} {
		if err := store.AppendMessage(ctx, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	// Disabled (0 days) must delete nothing.
	cfgDisabled := config.DefaultConfig()
	cfgDisabled.Voice.TranscriptRetentionDays = 0
	cDisabled := NewCollector(store, cfgDisabled)
	if deleted := cDisabled.RunVoiceTranscriptCleanup(ctx); deleted != 0 {
		t.Fatalf("expected 0 deleted when retention disabled, got %d", deleted)
	}

	// Enabled (7 days) deletes only the 10-day-old voice transcript.
	cfgEnabled := config.DefaultConfig()
	cfgEnabled.Voice.TranscriptRetentionDays = 7
	cEnabled := NewCollector(store, cfgEnabled)
	results := cEnabled.RunAll(ctx)
	if results.OldVoiceTranscripts != 1 {
		t.Fatalf("expected 1 old voice transcript deleted, got %d", results.OldVoiceTranscripts)
	}

	if msgs, _, err := store.GetMessages(ctx, "voice-gc", "fresh-voice-thread", 10, ""); err != nil || len(msgs) != 1 {
		t.Fatalf("expected fresh voice transcript preserved, got len=%d err=%v", len(msgs), err)
	}
	if msgs, _, err := store.GetMessages(ctx, "voice-gc", "old-text-thread", 10, ""); err != nil || len(msgs) != 1 {
		t.Fatalf("expected non-voice text message preserved, got len=%d err=%v", len(msgs), err)
	}
}
