package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"mailio/internal/app"
	"mailio/internal/config"
	"mailio/internal/dns"
	"mailio/internal/letsencrypt"
	"mailio/internal/opendkim"
	"mailio/internal/opendmarc"
	"mailio/internal/postfix"
	"mailio/internal/sasl"
	"mailio/internal/util"
)

func setupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Make the container match config.yml",
		Long: "Generates any missing DKIM keys, publishes the DKIM, SPF and DMARC records\n" +
			"through the DNS API, writes the Postfix, OpenDKIM, OpenDMARC and SASL\n" +
			"configuration, and creates the maildirs.\n\n" +
			"The entrypoint runs this on every start, and it is idempotent: existing\n" +
			"keys, certificates and correct DNS records are left alone. Running it by\n" +
			"hand is how you apply a config.yml edit without restarting, though a\n" +
			"restart does the same thing.",
		Args:    cobra.NoArgs,
		Example: "  docker exec " + app.Name + " " + app.Name + " setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetup(cmd)
		},
	}
}

func runSetup(cmd *cobra.Command) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	cmd.PrintErrln("[setup] generating DKIM keys...")
	pubkeys := make(map[string]string)
	for _, d := range cfg.Domains {
		pk, err := opendkim.GenerateKey(d.Name, d.Selector)
		if err != nil {
			return fmt.Errorf("generate dkim key for %s: %w", d.Name, err)
		}
		pubkeys[d.Name] = pk
	}

	cmd.PrintErrln("[setup] syncing DNS records...")
	allRecords, err := dns.GetAll()
	if err != nil {
		return fmt.Errorf("get dns records: %w", err)
	}
	for _, d := range cfg.Domains {
		if err := dns.UpsertDKIM(d.Name, d.Selector, pubkeys[d.Name], allRecords); err != nil {
			return fmt.Errorf("upsert dkim record for %s: %w", d.Name, err)
		}
		if err := dns.UpsertSPF(d.Name, cfg.Hostname, d.SPF, allRecords); err != nil {
			return fmt.Errorf("upsert spf record for %s: %w", d.Name, err)
		}
		if err := dns.UpsertDMARC(d.Name, d.DMARC, allRecords); err != nil {
			return fmt.Errorf("upsert dmarc record for %s: %w", d.Name, err)
		}
	}

	cmd.PrintErrln("[setup] writing Postfix config...")
	if err := ensureCert(cmd, cfg); err != nil {
		return err
	}
	if err := postfix.WriteMainCF(cfg); err != nil {
		return fmt.Errorf("write main.cf: %w", err)
	}
	if err := postfix.WriteMasterCF(); err != nil {
		return fmt.Errorf("write master.cf: %w", err)
	}
	if err := postfix.WriteVirtualMaps(cfg.Domains); err != nil {
		return fmt.Errorf("write virtual maps: %w", err)
	}
	if err := postfix.WriteSenderLogin(cfg.Domains); err != nil {
		return fmt.Errorf("write sender login maps: %w", err)
	}
	if postfix.HasRelay(cfg) {
		if err := postfix.WriteRelay(cfg); err != nil {
			return fmt.Errorf("write relay maps: %w", err)
		}
	}
	if err := postfix.SetupMaildirs(cfg.Domains); err != nil {
		return fmt.Errorf("setup maildirs: %w", err)
	}
	if err := util.Run("newaliases"); err != nil {
		return fmt.Errorf("newaliases: %w", err)
	}

	cmd.PrintErrln("[setup] writing OpenDKIM config...")
	if err := opendkim.WriteConf(cfg.Hostname); err != nil {
		return fmt.Errorf("write opendkim.conf: %w", err)
	}
	if err := opendkim.WriteTables(cfg.Domains); err != nil {
		return fmt.Errorf("write opendkim tables: %w", err)
	}
	if err := opendkim.WriteTrustedHosts(cfg.MyNetworks); err != nil {
		return fmt.Errorf("write trusted hosts: %w", err)
	}

	cmd.PrintErrln("[setup] writing OpenDMARC config...")
	if err := opendmarc.WriteConf(cfg.Hostname); err != nil {
		return fmt.Errorf("write opendmarc.conf: %w", err)
	}

	cmd.PrintErrln("[setup] configuring SASL...")
	if err := sasl.WriteConf(); err != nil {
		return fmt.Errorf("write sasl conf: %w", err)
	}
	if err := sasl.SetupUsers(cfg); err != nil {
		return fmt.Errorf("setup sasl users: %w", err)
	}

	if err := prepareRunDirs(); err != nil {
		return err
	}

	cmd.PrintErrln("[setup] done")
	return nil
}

// ensureCert obtains a Let's Encrypt certificate when one is configured, and
// falls back to a self-signed one when it cannot.
//
// A cert failure must not take down the whole mail server: without this, a rate
// limit or a DNS hiccup is the difference between a server that warns and a
// server that will not start. The renewal loop keeps retrying in the background.
func ensureCert(cmd *cobra.Command, cfg *config.Config) error {
	if cfg.LetsEncrypt == nil {
		if err := postfix.GenerateSelfSignedCert(cfg.Hostname); err != nil {
			return fmt.Errorf("generate tls cert: %w", err)
		}
		return nil
	}
	_, err := letsencrypt.Obtain(cfg, postfix.TLSCertFile, postfix.TLSKeyFile, false)
	if err == nil {
		return nil
	}
	cmd.PrintErrf("[setup] WARNING: could not obtain Let's Encrypt cert: %v\n", err)
	cmd.PrintErrln("[setup] continuing with fallback cert; renewal loop will retry")
	if err := postfix.GenerateSelfSignedCert(cfg.Hostname); err != nil {
		return fmt.Errorf("generate fallback tls cert: %w", err)
	}
	return nil
}

// prepareRunDirs creates the sockets' parent directories with the ownership each
// daemon drops privileges to. Both daemons refuse to start without them.
func prepareRunDirs() error {
	if err := os.MkdirAll("/run/opendkim", 0755); err != nil {
		return fmt.Errorf("mkdir /run/opendkim: %w", err)
	}
	if err := util.Run("chown", "opendkim:opendkim", "/run/opendkim"); err != nil {
		return fmt.Errorf("chown /run/opendkim: %w", err)
	}
	// The opendmarc package's user has primary group "mail" (no "opendmarc"
	// group exists), so chown to opendmarc:mail.
	if err := os.MkdirAll(opendmarc.RunDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", opendmarc.RunDir, err)
	}
	if err := util.Run("chown", "opendmarc:mail", opendmarc.RunDir); err != nil {
		return fmt.Errorf("chown %s: %w", opendmarc.RunDir, err)
	}
	return nil
}
