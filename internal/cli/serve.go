package cli

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"mailio/internal/api"
	"mailio/internal/app"
	"mailio/internal/backup"
	"mailio/internal/config"
	"mailio/internal/store"
)

// webDir is where the image puts the built admin interface; web/dist is where a checkout's own
// build lands.
const webDir = "/srv/web"

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, st, log, err := setup()
			if err != nil {
				return err
			}
			defer st.Close()

			log.Info("starting", "version", app.Version)
			if cfg.PublicURL.Scheme != "https" {
				log.Warn("the public url is http, so the session cookie goes without Secure", "url", cfg.PublicURL.String())
			}
			if err := firstRun(cmd.Context(), cfg, st, log); err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			srv := &http.Server{
				Addr:              app.ListenAddr,
				Handler:           api.New(cfg, log, st, web(log)),
				ReadHeaderTimeout: 10 * time.Second,
			}

			// Waited on below, so nothing is still writing when the database closes.
			var background sync.WaitGroup
			background.Go(func() { sweep(ctx, st, log) })
			background.Go(func() { backup.Run(ctx, st, cfg.BackupURL, cfg.DataDir, log) })

			errc := make(chan error, 1)
			go func() {
				log.Info("listening", "addr", srv.Addr, "backups", cfg.BackupURL != "")
				errc <- srv.ListenAndServe()
			}()

			select {
			case err := <-errc:
				stop()
				background.Wait()
				return err
			case <-ctx.Done():
			}
			log.Info("stopping")
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err = srv.Shutdown(shutdown)
			background.Wait()
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			return err
		},
	}
}

// firstRun logs an admin link when nobody can sign in to the admin interface.
//
// No default password exists at any point, so there is no credential to forget to change. A new
// link each start, because a stored one cannot be shown again.
func firstRun(ctx context.Context, cfg *config.Config, st *store.Store, log *slog.Logger) error {
	has, err := st.HasAdmin(ctx)
	if err != nil || has {
		return err
	}
	token, expires, err := st.CreateAdminLink(ctx)
	if err != nil {
		return err
	}
	log.Info("no admin yet: open this link to make one",
		"link", cfg.Link(api.AdminLinkPath(token)), "expires_at", expires.Format(time.RFC3339))
	return nil
}

// web finds the built admin interface, or nil for the placeholder.
func web(log *slog.Logger) fs.FS {
	for _, dir := range []string{webDir, "web/dist"} {
		if _, err := os.Stat(dir); err == nil {
			return os.DirFS(dir)
		}
	}
	log.Warn("no admin interface is built: browsers get the placeholder page", "dir", webDir)
	return nil
}

// sweep collects lapsed sessions and admin links, which are already unusable.
func sweep(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		sessions, err1 := st.SweepSessions(ctx)
		links, err2 := st.SweepAdminLinks(ctx)
		if err := errors.Join(err1, err2); err != nil && ctx.Err() == nil {
			log.Error("sweep failed", "error", err)
		} else if sessions+links > 0 {
			log.Info("swept", "sessions", sessions, "admin_links", links)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
