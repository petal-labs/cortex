package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// Backup creates a consistent backup of the database to the specified path.
// This uses SQLite's online backup API for hot backups without downtime.
func (b *Backend) Backup(ctx context.Context, destPath string) error {
	// Get the underlying *sql.DB connection
	conn, err := b.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Close()

	// Use raw connection to access SQLite backup API
	err = conn.Raw(func(driverConn interface{}) error {
		srcConn, ok := driverConn.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("failed to get SQLite connection")
		}

		// Open destination database
		destDB, err := sql.Open("sqlite3", destPath)
		if err != nil {
			return fmt.Errorf("failed to open destination database: %w", err)
		}
		defer destDB.Close()

		// Get destination connection
		destConnCtx, err := destDB.Conn(ctx)
		if err != nil {
			return fmt.Errorf("failed to get destination connection: %w", err)
		}
		defer destConnCtx.Close()

		return destConnCtx.Raw(func(destDriverConn interface{}) error {
			destConn, ok := destDriverConn.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("failed to get destination SQLite connection")
			}

			// Perform the backup
			backup, err := destConn.Backup("main", srcConn, "main")
			if err != nil {
				return fmt.Errorf("failed to initialize backup: %w", err)
			}
			defer backup.Finish()

			// Step through the backup (copy all pages)
			for {
				done, err := backup.Step(-1)
				if err != nil {
					return fmt.Errorf("backup step failed: %w", err)
				}
				if done {
					break
				}
			}

			return nil
		})
	})

	return err
}

// BackupProgress represents the progress of a backup operation.
type BackupProgress struct {
	Remaining int // Pages remaining
	Total     int // Total pages
}

// BackupWithProgress creates a backup with progress callback.
// The progress function is called periodically during the backup.
func (b *Backend) BackupWithProgress(ctx context.Context, destPath string, progress func(BackupProgress)) error {
	conn, err := b.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Close()

	err = conn.Raw(func(driverConn interface{}) error {
		srcConn, ok := driverConn.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("failed to get SQLite connection")
		}

		destDB, err := sql.Open("sqlite3", destPath)
		if err != nil {
			return fmt.Errorf("failed to open destination database: %w", err)
		}
		defer destDB.Close()

		destConnCtx, err := destDB.Conn(ctx)
		if err != nil {
			return fmt.Errorf("failed to get destination connection: %w", err)
		}
		defer destConnCtx.Close()

		return destConnCtx.Raw(func(destDriverConn interface{}) error {
			destConn, ok := destDriverConn.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("failed to get destination SQLite connection")
			}

			backup, err := destConn.Backup("main", srcConn, "main")
			if err != nil {
				return fmt.Errorf("failed to initialize backup: %w", err)
			}
			defer backup.Finish()

			// Step through the backup in chunks for progress reporting
			const pagesPerStep = 100
			for {
				done, err := backup.Step(pagesPerStep)
				if err != nil {
					return fmt.Errorf("backup step failed: %w", err)
				}

				// Report progress
				if progress != nil {
					progress(BackupProgress{
						Remaining: backup.Remaining(),
						Total:     backup.PageCount(),
					})
				}

				// Check for context cancellation
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				if done {
					break
				}
			}

			return nil
		})
	})

	return err
}
