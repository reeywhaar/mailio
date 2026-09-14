// Package cli is mailio's command line: the setup the entrypoint runs on every
// start, and the few things an operator needs a shell inside the container for.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"mailio/internal/app"
	"mailio/internal/config"
)

func root() *cobra.Command {
	cmd := &cobra.Command{
		Use:   app.Name,
		Short: "A Postfix mail server that configures itself from one YAML file",
		Long: "mailio reads config.yml and makes the container match it: DKIM keys, the\n" +
			"DNS records that publish them, Postfix, OpenDKIM, OpenDMARC and the SASL\n" +
			"logins that authenticate submission.\n\n" +
			"The entrypoint runs `setup` on every start, so the file is the source of\n" +
			"truth and a restart is how you apply an edit. The commands here are for\n" +
			"the things a restart is too blunt for.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Cobra's Print helpers write to stderr unless an output is set, which would make
	// `PW=$(mailio config show)` capture nothing at all.
	cmd.SetOut(os.Stdout)
	cmd.AddCommand(setupCmd(), accountCmd(), configCmd(), certCmd(), healthcheckCmd(), versionCmd())
	return cmd
}

// Execute runs the command line and returns a process exit code.
func Execute() int {
	if err := root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, app.Name+":", err)
		return 1
	}
	return 0
}

// group finishes a command that only holds subcommands.
//
// Cobra returns help for a command with no Run of its own *before* it validates
// arguments, so `mailio cert bogus` would print the cert help and exit 0 — the
// same silent mishandling of a typo the hand-rolled dispatch used to have.
// Giving the group something to run makes cobra validate first, and NoArgs then
// refuses anything that is not one of its subcommands. With no arguments it
// still prints help, which is what a bare group should do.
func group(cmd *cobra.Command) *cobra.Command {
	cmd.Args = cobra.NoArgs
	cmd.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
	return cmd
}

// loadConfig is the first thing every command here does. It names the path in
// the error because the commonest way for this to fail is the volume not being
// mounted, and "no such file" on its own does not say which file.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", config.Path, err)
	}
	return cfg, nil
}

// accountAddr is the SMTP-AUTH username and the envelope sender an account owns:
// the full address, since the same local part can exist in several domains.
func accountAddr(domain string, acc config.Account) string {
	return acc.LocalPart + "@" + domain
}

// relayFor resolves the smarthost an account's mail leaves through, most
// specific first, the same order Postfix's sender-dependent routing applies.
func relayFor(cfg *config.Config, d config.Domain, acc config.Account) *config.Relay {
	switch {
	case acc.Relay != nil:
		return acc.Relay
	case d.Relay != nil:
		return d.Relay
	default:
		return cfg.Relay
	}
}
