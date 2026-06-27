// Package cli wires the mailio subcommands together and runs the default setup.
package cli

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"mailio/internal/config"
	"mailio/internal/dns"
	"mailio/internal/letsencrypt"
	"mailio/internal/opendkim"
	"mailio/internal/opendmarc"
	"mailio/internal/postfix"
	"mailio/internal/sasl"
	"mailio/internal/util"
)

// renewInterval is how often the renewal loop checks the certificate when the
// current cert is healthy. Renewal itself only happens when the cert is within
// 30 days of expiry. renewRetryInterval is the (much shorter) backoff used after
// a failed attempt, so a startup fallback or transient ACME error recovers
// quickly instead of waiting a full day.
const (
	renewInterval      = 24 * time.Hour
	renewRetryInterval = 15 * time.Minute
)

// Run dispatches to a subcommand based on args (os.Args), defaulting to setup.
func Run(args []string) {
	if len(args) >= 2 && args[1] == "config" {
		switch {
		case len(args) == 2:
			configPrint()
		case args[2] == "mutt":
			configMutt()
		case args[2] == "monit":
			configMonit()
		default:
			fmt.Fprintf(os.Stderr, "unknown config subcommand %q (want: mutt, monit, or none)\n", args[2])
			os.Exit(1)
		}
		return
	}
	if len(args) >= 2 && args[1] == "add-account" {
		addAccount(args)
		return
	}
	if len(args) >= 2 && args[1] == "delete-account" {
		deleteAccount(args)
		return
	}
	if len(args) >= 3 && args[1] == "cert" && args[2] == "renew-loop" {
		certRenewLoop()
		return
	}
	if len(args) >= 3 && args[1] == "cert" && args[2] == "renew" {
		certRenew(args)
		return
	}
	setup()
}

func must(label string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "[setup] %s: %v\n", label, err)
		os.Exit(1)
	}
}

func setup() {
	cfg, err := config.Load()
	must("load config", err)

	fmt.Println("[setup] generating DKIM keys...")
	pubkeys := make(map[string]string)
	for _, d := range cfg.Domains {
		pk, err := opendkim.GenerateKey(d.Name, d.Selector)
		must("generate dkim key for "+d.Name, err)
		pubkeys[d.Name] = pk
	}

	fmt.Println("[setup] syncing DNS records...")
	allRecords, err := dns.GetAll()
	must("get dns records", err)
	for _, d := range cfg.Domains {
		must("upsert dkim record for "+d.Name,
			dns.UpsertDKIM(d.Name, d.Selector, pubkeys[d.Name], allRecords))
		must("upsert spf record for "+d.Name,
			dns.UpsertSPF(d.Name, cfg.Hostname, d.SPF, allRecords))
		must("upsert dmarc record for "+d.Name,
			dns.UpsertDMARC(d.Name, d.DMARC, allRecords))
	}

	fmt.Println("[setup] writing Postfix config...")
	if cfg.LetsEncrypt != nil {
		// A cert failure must not take down the whole mail server. Log it and fall
		// back to a self-signed cert if none exists yet, so Postfix can still start;
		// the renewal loop keeps retrying Let's Encrypt in the background.
		if _, err := letsencrypt.Obtain(cfg, postfix.TLSCertFile, postfix.TLSKeyFile, false); err != nil {
			fmt.Fprintf(os.Stderr, "[setup] WARNING: could not obtain Let's Encrypt cert: %v\n", err)
			fmt.Fprintln(os.Stderr, "[setup] continuing with fallback cert; renewal loop will retry")
			must("generate fallback tls cert", postfix.GenerateSelfSignedCert(cfg.Hostname))
		}
	} else {
		must("generate tls cert", postfix.GenerateSelfSignedCert(cfg.Hostname))
	}
	must("write main.cf", postfix.WriteMainCF(cfg))
	must("write master.cf", postfix.WriteMasterCF())
	must("write virtual maps", postfix.WriteVirtualMaps(cfg.Domains))
	must("write sender login maps", postfix.WriteSenderLogin(cfg.Domains))
	if postfix.HasRelay(cfg) {
		must("write relay maps", postfix.WriteRelay(cfg))
	}
	must("setup maildirs", postfix.SetupMaildirs(cfg.Domains))
	must("newaliases", util.Run("newaliases"))

	fmt.Println("[setup] writing OpenDKIM config...")
	must("write opendkim.conf", opendkim.WriteConf(cfg.Hostname))
	must("write opendkim tables", opendkim.WriteTables(cfg.Domains))
	must("write trusted hosts", opendkim.WriteTrustedHosts(cfg.MyNetworks))

	fmt.Println("[setup] writing OpenDMARC config...")
	must("write opendmarc.conf", opendmarc.WriteConf(cfg.Hostname))

	fmt.Println("[setup] configuring SASL...")
	must("write sasl conf", sasl.WriteConf())
	must("setup sasl users", sasl.SetupUsers(cfg))

	must("mkdir /run/opendkim", os.MkdirAll("/run/opendkim", 0755))
	must("chown /run/opendkim", util.Run("chown", "opendkim:opendkim", "/run/opendkim"))

	// The opendmarc package's user has primary group "mail" (no "opendmarc"
	// group exists), so chown to opendmarc:mail.
	must("mkdir "+opendmarc.RunDir, os.MkdirAll(opendmarc.RunDir, 0755))
	must("chown "+opendmarc.RunDir, util.Run("chown", "opendmarc:mail", opendmarc.RunDir))

	fmt.Println("[setup] done")
}

func configMutt() {
	cfg, err := config.Load()
	must("load config", err)

	type entry struct {
		from     string
		smtpUser string
		password string
	}
	var accounts []entry
	for _, d := range cfg.Domains {
		for _, acc := range d.Accounts {
			addr := acc.LocalPart + "@" + d.Name
			accounts = append(accounts, entry{
				from: addr,
				// SMTP-AUTH username is the full address; @ must be %40-encoded
				// inside the smtp_url so mutt doesn't read it as the host part.
				smtpUser: strings.ReplaceAll(addr, "@", "%40"),
				password: strings.ReplaceAll(acc.Password, `"`, `\"`),
			})
		}
	}
	if len(accounts) == 0 {
		fmt.Fprintln(os.Stderr, "[mutt] no accounts found in config")
		os.Exit(1)
	}

	first := accounts[0]
	fmt.Printf("# Generated by mailio config mutt\n")
	fmt.Printf("# Server: %s\n\n", cfg.Hostname)
	fmt.Printf("set from      = \"%s\"\n", first.from)
	fmt.Printf("set smtp_url  = \"smtp://%s@%s:587/\"\n", first.smtpUser, cfg.Hostname)
	fmt.Printf("set smtp_pass = \"%s\"\n", first.password)

	if len(accounts) > 1 {
		fmt.Printf("\n# Additional accounts:\n")
		for _, acc := range accounts[1:] {
			fmt.Printf("\n# --- %s ---\n", acc.from)
			fmt.Printf("# set from      = \"%s\"\n", acc.from)
			fmt.Printf("# set smtp_url  = \"smtp://%s@%s:587/\"\n", acc.smtpUser, cfg.Hostname)
			fmt.Printf("# set smtp_pass = \"%s\"\n", acc.password)
		}
	}
}

// configMonit prints a monit-compatible mail config to stdout. monit typically
// runs on the mail host and submits over loopback, so it points at
// localhost:587 without TLS (the password never leaves the host). The 'from'
// address must equal the SMTP-AUTH username because of the sender/login binding.
func configMonit() {
	cfg, err := config.Load()
	must("load config", err)
	if err := renderMonit(os.Stdout, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[monit] %v\n", err)
		os.Exit(1)
	}
}

func renderMonit(w io.Writer, cfg *config.Config) error {
	type entry struct {
		addr     string
		password string
	}
	var accounts []entry
	for _, d := range cfg.Domains {
		for _, acc := range d.Accounts {
			accounts = append(accounts, entry{
				addr:     acc.LocalPart + "@" + d.Name,
				password: strings.ReplaceAll(acc.Password, `"`, `\"`),
			})
		}
	}
	if len(accounts) == 0 {
		return fmt.Errorf("no accounts found in config")
	}

	first := accounts[0]
	fmt.Fprintf(w, "# Generated by mailio config monit\n")
	fmt.Fprintf(w, "# Add to /etc/monitrc (or /etc/monit/monitrc) and run: monit reload\n")
	fmt.Fprintf(w, "# monit must run on the mail host (submits over localhost).\n\n")
	fmt.Fprintf(w, "set mailserver localhost port 587\n")
	fmt.Fprintf(w, "    username \"%s\" password \"%s\"\n\n", first.addr, first.password)
	fmt.Fprintf(w, "set mail-format { from: Monit <%s> }\n\n", first.addr)
	fmt.Fprintf(w, "# Set your alert recipient(s), e.g.:\n")
	fmt.Fprintf(w, "# set alert you@example.com\n")

	if len(accounts) > 1 {
		fmt.Fprintf(w, "\n# Additional accounts (monit uses a single mailserver/from pair):\n")
		for _, acc := range accounts[1:] {
			fmt.Fprintf(w, "\n# --- %s ---\n", acc.addr)
			fmt.Fprintf(w, "# set mailserver localhost port 587\n")
			fmt.Fprintf(w, "#     username \"%s\" password \"%s\"\n", acc.addr, acc.password)
			fmt.Fprintf(w, "# set mail-format { from: Monit <%s> }\n", acc.addr)
		}
	}
	return nil
}

// configPrint lists every account's SMTP credentials, one block per account.
func configPrint() {
	cfg, err := config.Load()
	must("load config", err)
	if err := renderConfig(os.Stdout, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[config] %v\n", err)
		os.Exit(1)
	}
}

func renderConfig(w io.Writer, cfg *config.Config) error {
	var printed bool
	for _, d := range cfg.Domains {
		for _, acc := range d.Accounts {
			if printed {
				fmt.Fprintln(w)
			}
			printed = true
			printCreds(w, cfg.Hostname, acc.LocalPart+"@"+d.Name, acc.Password)
		}
	}
	if !printed {
		return fmt.Errorf("no accounts found in config")
	}
	return nil
}

// printCreds writes the email/smtp_url/password block for one account.
func printCreds(w io.Writer, hostname, addr, password string) {
	fmt.Fprintf(w, "email: %s\n", addr)
	fmt.Fprintf(w, "smtp_url: %s\n", smtpURL(hostname, addr))
	fmt.Fprintf(w, "password: %s\n", password)
}

// smtpURL builds the submission URL for an account. The username is the full
// address, so its @ is %40-encoded to keep it out of the host part.
func smtpURL(hostname, addr string) string {
	return fmt.Sprintf("smtp://%s@%s:587/", strings.ReplaceAll(addr, "@", "%40"), hostname)
}

// addAccount appends an account to a domain in config.yml, generating a
// password when one isn't supplied, then prints the new credentials.
//
//	add-account <domain> <local_part> [--password <pw>] [--local_user <user>]
func addAccount(args []string) {
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: mailio add-account <domain> <local_part> [--password <pw>] [--local_user <user>]")
		os.Exit(1)
	}
	domain, localPart := args[2], args[3]

	var password, localUser string
	for i := 4; i < len(args); i++ {
		key, val, hasInline := strings.Cut(args[i], "=")
		next := func() string {
			if hasInline {
				return val
			}
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "missing value for %s\n", key)
				os.Exit(1)
			}
			return args[i]
		}
		switch key {
		case "--password":
			password = next()
		case "--local_user":
			localUser = next()
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", args[i])
			os.Exit(1)
		}
	}

	cfg, err := config.Load()
	must("load config", err)

	if password == "" {
		password, err = generatePassword()
		must("generate password", err)
	}

	must("add account", config.AddAccount(domain, localPart, password, localUser))

	// Provision live (best-effort): create the SASL login, then refresh maps and
	// reload. Outside the running container this fails harmlessly — a restart
	// re-runs setup and provisions from config.
	applied := finishLive(sasl.AddUser(domain, localPart, password))

	fmt.Fprintf(os.Stderr, "[add-account] added %s@%s\n\n", localPart, domain)
	printCreds(os.Stdout, cfg.Hostname, localPart+"@"+domain, password)
	if applied != nil {
		fmt.Fprintf(os.Stderr, "\n[add-account] could not apply live: %v\n", applied)
		fmt.Fprintln(os.Stderr, "[add-account] restart mailio to apply (docker compose restart mailio).")
	}
}

// finishLive applies account changes to the running server: if the SASL op
// succeeded, it regenerates the routing maps from the (already-updated) config
// and reloads postfix. Returns the first error, or nil if fully applied. All
// steps only work inside the running container.
func finishLive(saslErr error) error {
	if saslErr != nil {
		return saslErr
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := postfix.WriteVirtualMaps(cfg.Domains); err != nil {
		return err
	}
	if err := postfix.WriteSenderLogin(cfg.Domains); err != nil {
		return err
	}
	if err := postfix.SetupMaildirs(cfg.Domains); err != nil {
		return err
	}
	return util.Run("postfix", "reload")
}

// deleteAccount removes an account from config.yml and revokes its SMTP login.
// The mailbox under /var/mail is intentionally kept (mail data is preserved);
// remove it by hand if you want the stored mail gone too.
//
//	delete-account <domain> <local_part>
func deleteAccount(args []string) {
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: mailio delete-account <domain> <local_part>")
		os.Exit(1)
	}
	domain, localPart := args[2], args[3]

	must("delete account", config.DeleteAccount(domain, localPart))

	// Revoke the login and reconcile routing live (best-effort). Outside the
	// running container this fails harmlessly — the next setup run rebuilds the
	// db and maps from config and drops the account anyway.
	applied := finishLive(sasl.DeleteUser(domain, localPart))

	fmt.Fprintf(os.Stderr, "[delete-account] removed %s@%s (mailbox under /var/mail kept)\n", localPart, domain)
	if applied != nil {
		fmt.Fprintf(os.Stderr, "[delete-account] could not apply live: %v\n", applied)
		fmt.Fprintln(os.Stderr, "[delete-account] restart mailio to apply (docker compose restart mailio).")
	}
}

// generatePassword returns a 20-character password from an unambiguous
// alphanumeric alphabet (no 0/O/1/l/I), drawn from a cryptographic source.
func generatePassword() (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

func certRenew(args []string) {
	force := len(args) >= 4 && args[3] == "--force"
	cfg, err := config.Load()
	must("load config", err)
	if cfg.LetsEncrypt == nil {
		fmt.Fprintln(os.Stderr, "[acme] letsencrypt not configured in config.yml")
		os.Exit(1)
	}
	_, err = letsencrypt.Obtain(cfg, postfix.TLSCertFile, postfix.TLSKeyFile, force)
	must("obtain certificate", err)
}

// certRenewLoop periodically checks the certificate and renews it when it nears
// expiry, reloading Postfix whenever a new certificate is written. It runs until
// the process is killed. If Let's Encrypt is not configured it returns
// immediately so the entrypoint can start it unconditionally.
func certRenewLoop() {
	cfg, err := config.Load()
	must("load config", err)
	if cfg.LetsEncrypt == nil {
		fmt.Println("[acme] letsencrypt not configured, renewal loop disabled")
		return
	}

	fmt.Printf("[acme] renewal loop started (checking every %s, retrying every %s after failure)\n", renewInterval, renewRetryInterval)
	interval := renewRetryInterval
	for {
		time.Sleep(interval)
		renewed, err := letsencrypt.Obtain(cfg, postfix.TLSCertFile, postfix.TLSKeyFile, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[acme] renewal check failed: %v (retrying in %s)\n", err, renewRetryInterval)
			interval = renewRetryInterval
			continue
		}
		interval = renewInterval
		if renewed {
			fmt.Println("[acme] certificate renewed, reloading postfix")
			if err := util.Run("postfix", "reload"); err != nil {
				fmt.Fprintf(os.Stderr, "[acme] postfix reload failed: %v\n", err)
			}
		}
	}
}
