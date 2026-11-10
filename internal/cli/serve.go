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
	"golang.org/x/crypto/acme"

	"mailio/internal/api"
	"mailio/internal/app"
	"mailio/internal/backup"
	"mailio/internal/certs"
	"mailio/internal/config"
	"mailio/internal/dnsapi"
	"mailio/internal/dnscheck"
	"mailio/internal/dnssync"
	"mailio/internal/imapd"
	"mailio/internal/outbound"
	"mailio/internal/smtpd"
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

			mgr, err := certs.New(ctx, st, cfg.MailHost, log)
			if err != nil {
				return err
			}
			tlsCfg := mgr.TLSConfig()
			if err := st.EnsureDKIM(ctx); err != nil {
				return err
			}
			sender := outbound.New(st, log, cfg.MailHost)
			// A staging run checks no records, so the admin interface is given nothing to ask, and says
			// none were checked.
			var keeper dnsKeeper
			var dnsStatus api.DNS
			if !cfg.Staging {
				keeper = dnsKeeper{dnscheck.NewChecker(st, dnscheck.Authoritative{}, cfg.MailHost), make(chan struct{}, 1)}
				dnsStatus = keeper
			}

			admin := &http.Server{
				Addr:              app.ListenAddr,
				Handler:           api.New(cfg, log, st, mailService{mgr, sender}, dnsStatus, web(log)),
				ReadHeaderTimeout: 10 * time.Second,
			}
			imap := imapd.New(st, log)
			inbound := smtpd.NewInbound(st, log, cfg.MailHost, smtpd.Checks{Host: cfg.MailHost}).Server(tlsCfg)
			inbound.Addr = app.SMTPAddr
			sub := smtpd.NewSubmission(st, log, cfg.MailHost, sender)
			submission := sub.Server(tlsCfg)
			submission.Addr = app.SubmissionAddr
			submissions := sub.Server(tlsCfg)
			submissions.Addr = app.SubmissionsAddr
			imapLn, err := imapd.Listen(app.IMAPAddr, tlsCfg)
			if err != nil {
				return err
			}

			// Waited on below, so nothing is still writing when the database closes.
			var background sync.WaitGroup
			background.Go(func() { sweep(ctx, st, log) })
			background.Go(func() { backup.Run(ctx, st, cfg.BackupURL, cfg.DataDir, log) })
			background.Go(func() { sender.Run(ctx) })
			var dnsAPI *dnsapi.Client
			if cfg.UsesDNSAPI() {
				dnsAPI = dnsapi.New(cfg.DNSAPI)
				background.Go(func() { (&certs.ACME{M: mgr, API: dnsAPI, Directory: acme.LetsEncryptURL}).Run(ctx) })
			}
			switch {
			case cfg.Staging:
				log.Warn("staging run: Let's Encrypt, the DNS API and the DNS record checks are off, and the mail ports keep a self-signed certificate")
			case dnsAPI == nil:
				log.Warn("no DNS API: publish the DNS records shown in the admin interface by hand, and the mail ports keep a self-signed certificate")
			}
			if !cfg.Staging {
				background.Go(func() { keepDNS(ctx, st, dnsAPI, keeper, cfg.MailHost, log) })
			}

			errc := make(chan error, 5)
			go func() { errc <- admin.ListenAndServe() }()
			go func() { errc <- inbound.ListenAndServe() }()
			go func() { errc <- submission.ListenAndServe() }()
			go func() { errc <- submissions.ListenAndServeTLS() }()
			go func() { errc <- imap.Serve(imapLn) }()
			log.Info("listening", "admin", app.ListenAddr, "smtp", app.SMTPAddr, "submission", app.SubmissionAddr,
				"submissions", app.SubmissionsAddr, "imap", app.IMAPAddr, "mail_host", cfg.MailHost,
				"backups", cfg.BackupURL != "")

			select {
			case err = <-errc:
				log.Error("a listener stopped", "error", err)
			case <-ctx.Done():
			}
			stop()
			log.Info("stopping")
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			admin.Shutdown(shutdown)
			inbound.Shutdown(shutdown)
			submission.Shutdown(shutdown)
			submissions.Shutdown(shutdown)
			imap.Close()
			background.Wait()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
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

// sweep collects lapsed sessions and admin links, which are already unusable, and empties each
// account's Trash and Junk of what they have kept long enough.
func sweep(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		sessions, err1 := st.SweepSessions(ctx)
		links, err2 := st.SweepAdminLinks(ctx)
		trash, junk, err3 := st.EmptyTrashAndJunk(ctx)
		if err := errors.Join(err1, err2, err3); err != nil && ctx.Err() == nil {
			log.Error("sweep failed", "error", err)
		} else if sessions+links > 0 || trash+junk > 0 {
			log.Info("swept", "sessions", sessions, "admin_links", links, "trash", trash, "junk", junk)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// mailService is the running mail server, as the admin interface sees it.
type mailService struct {
	certs  *certs.Manager
	sender *outbound.Sender
}

func (m mailService) Certificate() *store.Certificate { return m.certs.Current() }
func (m mailService) TestRelay(ctx context.Context, r *store.Relay) error {
	return m.sender.TestRelay(ctx, r)
}
func (m mailService) Wake() { m.sender.Wake() }

// dnsKeeper is the domains' records as the admin interface sees them: the last check of each, and
// a nudge for keepDNS when one may have changed.
type dnsKeeper struct {
	*dnscheck.Checker
	wake chan struct{}
}

func (k dnsKeeper) Changed() {
	select {
	case k.wake <- struct{}{}:
	default:
	}
}

// keepDNS publishes the domains' records through the DNS API when there is one, then checks what
// DNS serves for them: at start, every ten minutes, and right after a change in the admin
// interface, so a new domain's DKIM key is out within seconds.
func keepDNS(ctx context.Context, st *store.Store, api *dnsapi.Client, k dnsKeeper, host string, log *slog.Logger) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		if api != nil {
			n, err := dnssync.Sync(ctx, st, api, host, log)
			if err != nil && ctx.Err() == nil {
				log.Error("publishing dns records failed", "error", err)
			}
			// The zone's nameservers take a moment to serve what was just published.
			if n > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
				}
			}
		}
		if err := k.CheckAll(ctx); err != nil && ctx.Err() == nil {
			log.Error("checking dns records failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-k.wake:
		}
	}
}
