package pgvector

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUniqueConstraintError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if isUniqueConstraintError(nil) {
			t.Error("expected false for nil error")
		}
	})

	t.Run("typed PgError with 23505", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Code:    "23505",
			Message: "duplicate key value violates unique constraint",
		}
		if !isUniqueConstraintError(pgErr) {
			t.Error("expected true for PgError with code 23505")
		}
	})

	t.Run("wrapped PgError with 23505", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "23505", Message: "dup"}
		wrapped := fmt.Errorf("failed to create collection: %w", pgErr)
		if !isUniqueConstraintError(wrapped) {
			t.Error("expected true for wrapped PgError with code 23505")
		}
	})

	t.Run("typed PgError with other code", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Code:    "42P01",
			Message: "relation does not exist",
		}
		if isUniqueConstraintError(pgErr) {
			t.Error("expected false for PgError with non-23505 code")
		}
	})

	t.Run("localize-proof: non-English message with 23505 code", func(t *testing.T) {
		// The old string matcher looked for "duplicate key"/"unique
		// constraint" in the message text; the typed check keys on Code
		// alone, so localized messages classify correctly.
		pgErr := &pgconn.PgError{
			Code:    "23505",
			Message: "doppelte Schlüsselwerte verletzen Unique-Constraint",
		}
		if !isUniqueConstraintError(pgErr) {
			t.Error("expected true for localized message with 23505 code")
		}
	})

	t.Run("plain error whose text mentions 23505", func(t *testing.T) {
		// A non-pg error that merely contains the code in its text must not
		// be classified as a unique-constraint violation.
		err := errors.New("some log line mentioning 23505 but not a pg error")
		if isUniqueConstraintError(err) {
			t.Error("expected false for non-PgError with 23505 in text")
		}
	})

	t.Run("plain error with duplicate key text", func(t *testing.T) {
		err := errors.New("duplicate key value violates unique constraint")
		if isUniqueConstraintError(err) {
			t.Error("expected false for non-PgError with duplicate-key text")
		}
	})

	t.Run("ordinary error", func(t *testing.T) {
		if isUniqueConstraintError(errors.New("connection refused")) {
			t.Error("expected false for ordinary error")
		}
	})
}
