// Package sasl configures Cyrus SASL and provisions SMTP AUTH users.
package sasl

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"mailio/internal/config"
	"mailio/internal/util"
)

func WriteConf() error {
	if err := os.MkdirAll("/etc/sasl2", 0755); err != nil {
		return err
	}
	content := `pwcheck_method: auxprop
auxprop_plugin: sasldb
mech_list: PLAIN LOGIN
`
	fmt.Println("[sasl] wrote smtpd.conf")
	return os.WriteFile("/etc/sasl2/smtpd.conf", []byte(content), 0644)
}

const sasldbPath = "/etc/sasl2/sasldb2"

// SetupUsers rebuilds the SASL database from config: it deletes the existing db
// and recreates an entry for every account. Rebuilding (rather than upserting)
// means accounts removed from config are dropped on the next run, and makes the
// db fully derived from config.yml — so it does not need to be persisted.
//
// SASL identities are scoped per domain (realm = domain), so the SMTP-AUTH
// username is the full address <local_part>@<domain>. This keeps accounts with
// the same local_part in different domains distinct, each with its own password.
func SetupUsers(cfg *config.Config) error {
	// Start from a clean slate so purged accounts don't linger.
	if err := os.Remove(sasldbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reset sasldb: %w", err)
	}

	seen := make(map[string]bool)
	for _, d := range cfg.Domains {
		for _, acc := range d.Accounts {
			addr := acc.LocalPart + "@" + d.Name
			if seen[addr] {
				continue
			}
			seen[addr] = true
			if err := AddUser(d.Name, acc.LocalPart, acc.Password); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddUser provisions (or updates) a single SMTP-AUTH identity without touching
// the rest of the database, so it can run against a live db.
func AddUser(domain, localPart, password string) error {
	cmd := exec.Command("saslpasswd2", "-c", "-u", domain, "-p", localPart)
	cmd.Stdin = strings.NewReader(password)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("saslpasswd2 for %s@%s: %w", localPart, domain, err)
	}
	fmt.Printf("[sasl] upserted user %s (realm: %s)\n", localPart, domain)
	return fixDBPerms()
}

// DeleteUser removes a single SMTP-AUTH identity, revoking the login
// immediately without waiting for the next full rebuild.
func DeleteUser(domain, localPart string) error {
	if err := util.Run("saslpasswd2", "-d", "-u", domain, localPart); err != nil {
		return err
	}
	return fixDBPerms()
}

// fixDBPerms restricts the SASL db so only root and the postfix group can read
// it. No-op if the db doesn't exist (e.g. config has no accounts).
func fixDBPerms() error {
	if _, err := os.Stat(sasldbPath); err != nil {
		return nil
	}
	if err := util.Run("chown", "root:postfix", sasldbPath); err != nil {
		return err
	}
	return os.Chmod(sasldbPath, 0640)
}
