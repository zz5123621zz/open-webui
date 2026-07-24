package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
)

type onlineBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

// Backup creates a transactionally consistent SQLite snapshot without stopping
// the running service. It refuses to overwrite an existing backup.
func (s *Store) Backup(ctx context.Context, destination string) error {
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve backup path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	placeholder, err := os.OpenFile(absolute, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("backup destination already exists: %s", absolute)
	}
	if err != nil {
		return fmt.Errorf("reserve backup destination: %w", err)
	}
	if err := placeholder.Close(); err != nil {
		_ = os.Remove(absolute)
		return fmt.Errorf("close backup destination: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.Remove(absolute)
		}
	}()

	connection, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlite backup connection: %w", err)
	}
	defer connection.Close()
	if err := connection.Raw(func(raw any) error {
		backuper, ok := raw.(onlineBackuper)
		if !ok {
			return errors.New("sqlite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(absolute)
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for more := true; more; {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err = backup.Step(256)
			if err != nil {
				return err
			}
		}
		if err := backup.Finish(); err != nil {
			return err
		}
		finished = true
		return nil
	}); err != nil {
		return fmt.Errorf("create sqlite online backup: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open completed sqlite backup: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return fmt.Errorf("sync sqlite backup: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close sqlite backup: %w", closeErr)
	}
	success = true
	return nil
}
