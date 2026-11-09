// Package backup snapshots the database and posts it to a backio agent.
//
// mailio holds no credential, no provider, no bucket and no retention policy: it decides when to
// back up and what goes in the archive, and everything after that is the agent's. A compromised
// container cannot read, overwrite or delete a single existing backup, because it has nothing to
// authenticate with and nothing to point at.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mailio/internal/app"
	"mailio/internal/store"
)

// Interval is how often the loop looks, which is also a throttle: an afternoon in the admin
// interface is a handful of archives rather than one per click. Half an hour is the most anybody
// stands to lose.
const Interval = 30 * time.Minute

// uploadTimeout is the agent's own UPLOAD_TIMEOUT default. It answers once the archive is on the
// remote, so giving up sooner records a failure for an archive that is stored anyway.
const uploadTimeout = 30 * time.Minute

// Run posts an archive whenever the content has changed.
//
// Nothing else makes a copy happen: no timer, no heartbeat, no floor. An instance nobody has
// touched since Tuesday sends nothing, and liveness is /healthz's job.
func Run(ctx context.Context, st *store.Store, url, dataDir string, log *slog.Logger) {
	clearLeftovers(dataDir, log)
	if url == "" {
		return
	}
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()

	// A restart sends one: a process that has just started may be on a volume nobody has a
	// copy of yet.
	var sent uint64
	first := true

	for {
		if changes := st.Changes(); first || changes != sent {
			if err := send(ctx, st, url, dataDir); err != nil {
				if ctx.Err() == nil {
					log.Error("backup failed", "error", err)
				}
			} else {
				sent = changes
				first = false
				log.Info("backup sent", "changes", changes)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// scratch prefixes the directory a pass snapshots into.
const scratch = "backup-"

// clearLeftovers removes the scratch of a pass the process died in. Its deferred removal never
// ran, and nothing else would: each one is a whole snapshot left on the volume. Before the url
// check, so turning backups off does not keep what they left behind.
func clearLeftovers(dataDir string, log *slog.Logger) {
	left, _ := filepath.Glob(filepath.Join(dataDir, scratch+"*"))
	for _, dir := range left {
		if err := os.RemoveAll(dir); err != nil {
			log.Error("a backup's leftovers could not be removed", "dir", dir, "error", err)
		} else {
			log.Info("removed a backup's leftovers", "dir", dir)
		}
	}
}

func send(ctx context.Context, st *store.Store, url, dataDir string) error {
	dir, err := os.MkdirTemp(dataDir, scratch)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	snapshot := filepath.Join(dir, store.FileName)
	if err := st.SnapshotTo(ctx, snapshot); err != nil {
		return err
	}
	archive := filepath.Join(dir, "archive.tar.gz")
	if err := pack(snapshot, archive); err != nil {
		return err
	}
	// Sent from the file, so the pass holds a buffer rather than the database, twice.
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, f)
	if err != nil {
		return err
	}
	// A file is not a body net/http can size on its own, and an agent may refuse a chunked one.
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("User-Agent", app.Name+"/"+app.Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the agent answered %s", resp.Status)
	}
	return nil
}

// pack writes the snapshot to dst as a gzipped tar, streamed rather than read whole.
//
// Level 1: a pass long enough to overlap the next one is the thing that must not happen, and the
// levels above it spend CPU proportional to the file for a few percent.
//
// Members are mode 0600 and there are no directory entries, so extracting cannot re-chmod a data
// directory that already exists.
func pack(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	gz, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:    store.FileName,
		Mode:    0o600,
		Size:    info.Size(),
		ModTime: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if _, err := io.Copy(tw, in); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return out.Close()
}
