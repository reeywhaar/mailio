package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// SnapshotTo writes a consistent copy of the database to path, as one file that opens on its own.
//
// On a reader: a WAL reader sees one moment and blocks nobody, so mail keeps committing while a
// large copy is written. On the one writer, every delivery would wait for the copy to finish.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// VACUUM INTO refuses to overwrite.
	os.Remove(path)
	if _, err := s.reader.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	return nil
}
