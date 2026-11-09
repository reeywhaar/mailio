// Package store is the database: one file, every query, and the migrations that shape it.
//
// It is the only package that writes SQL. Handlers do not build queries and nothing reaches
// past it into the file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"mailio/internal/store/migrations"
)

// FileName is the one database. Mail will live in it too, which is what makes a backup one file.
const FileName = "mailio.db"

// Store holds two pools over the same file.
//
// WAL readers do not block the writer, so a second pool keeps a long read from queueing an
// incoming message behind it.
type Store struct {
	// writer is capped at one. SQLite permits one writer regardless, and holding that at the
	// pool rather than discovering it as SQLITE_BUSY is the difference between a queue and a
	// retry loop.
	writer *sql.DB
	reader *sql.DB

	// now is injectable so expiry is driven rather than slept through.
	now func() time.Time
}

const readers = 4

// pragmas are in the DSN rather than executed once, because the driver applies DSN pragmas to
// every connection it opens and a pool opens them whenever it likes.
var pragmas = []string{
	"busy_timeout(5000)",
	"journal_mode(WAL)",
	// FULL rather than NORMAL: once mailio has said 250 to a message the sender forgets it, so
	// a commit a power cut can undo is lost mail. See docs/storage.md.
	"synchronous(FULL)",
	"foreign_keys(1)",
	// Without this a single large write leaves a permanently huge -wal beside a database that
	// is mostly idle.
	"journal_size_limit(67108864)",
}

// Open opens the database in dir, applies migrations, and verifies its pragmas.
//
// dir must already exist and is never created — see docs/deploy.md.
func Open(dir string) (*Store, error) {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s does not exist: mount it, e.g. docker run -v mailio-data:%s", dir, dir)
	case err != nil:
		return nil, fmt.Errorf("%s: %w", dir, err)
	case !info.IsDir():
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	path := filepath.Join(dir, FileName)

	// IMMEDIATE takes the write lock at BEGIN. A transaction that reads and then writes would
	// otherwise fail outright, without waiting, when another process — the command line — holds
	// the lock at the moment it upgrades.
	writer, err := open(path, 1, "&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	reader, err := open(path, readers, "")
	if err != nil {
		writer.Close()
		return nil, err
	}

	s := &Store{writer: writer, reader: reader, now: time.Now}
	if err := migrations.Run(context.Background(), writer); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func open(path string, maxOpen int, extra string) (*sql.DB, error) {
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?"
	for i, p := range pragmas {
		if i > 0 {
			dsn += "&"
		}
		dsn += "_pragma=" + url.QueryEscape(p)
	}
	db, err := sql.Open("sqlite", dsn+extra)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(maxOpen)

	// A pragma is a request, not a guarantee. A foreign_keys that was silently ignored permits
	// orphaned rows for the life of the connection, so they are read back.
	if err := verify(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func verify(db *sql.DB) error {
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return fmt.Errorf("journal_mode is %q, not wal", mode)
	}
	var fk, sync int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		return err
	}
	if fk != 1 {
		return errors.New("foreign_keys is off")
	}
	if err := db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		return err
	}
	if sync != 2 {
		return fmt.Errorf("synchronous is %d, not FULL", sync)
	}
	return nil
}

// SetClock replaces the clock. Tests drive expiry with it.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// Now is the store's clock, in UTC.
func (s *Store) Now() time.Time { return s.now().UTC() }

// Ping reports whether the file answers.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.reader.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Close checkpoints the WAL and closes both pools.
//
// TRUNCATE empties the -wal, so a stopped instance leaves one whole database rather than three
// files that have to be copied together.
func (s *Store) Close() error {
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	if s.reader != nil {
		keep(s.reader.Close())
	}
	if s.writer != nil {
		// Best effort: the data is committed either way.
		if _, err := s.writer.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			keep(fmt.Errorf("checkpoint: %w", err))
		}
		keep(s.writer.Close())
	}
	return first
}

// execer is a pool or a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// queryer is a pool or a transaction.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// unix is how every timestamp is stored: seconds in an INTEGER column.
func unix(t time.Time) int64 { return t.UTC().Unix() }

func fromUnix(sec int64) time.Time { return time.Unix(sec, 0).UTC() }
