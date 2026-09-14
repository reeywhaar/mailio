package cli

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mailio/internal/app"
	"mailio/internal/config"
	"mailio/internal/postfix"
	"mailio/internal/sasl"
	"mailio/internal/util"
)

func accountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage the SMTP accounts this server hosts",
		Long: "An account is an SMTP-AUTH login and, optionally, a mailbox. The username\n" +
			"is the full address, so the same local part can exist in several domains\n" +
			"with independent passwords, and an account may only send mail as its own\n" +
			"address.\n\n" +
			"These commands edit config.yml and apply the change to the running server,\n" +
			"so there is nothing to restart.",
	}
	cmd.AddCommand(accountAddCmd(), accountListCmd(), accountDeleteCmd())
	return group(cmd)
}

func accountAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <domain> <local-part>",
		Short: "Add an account and print its credentials",
		Long: "The domain must already exist in config.yml. Without --password a strong\n" +
			"one is generated and printed.\n\n" +
			"Without --local-user the account is send-only: it can authenticate and\n" +
			"submit mail, but nothing is delivered to the address and mail sent to it\n" +
			"is refused. That is what an application sender wants; it also means the\n" +
			"account never sees its own bounces.",
		Args: cobra.ExactArgs(2),
		Example: "  docker exec " + app.Name + " " + app.Name + " account add example.com noreply\n" +
			"  docker exec " + app.Name + " " + app.Name + " account add example.com kate --local-user kate",
		RunE: func(cmd *cobra.Command, args []string) error {
			domain, localPart := args[0], args[1]
			password, _ := cmd.Flags().GetString("password")
			localUser, _ := cmd.Flags().GetString("local-user")

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if password == "" {
				if password, err = generatePassword(); err != nil {
					return fmt.Errorf("generate password: %w", err)
				}
			}
			if err := config.AddAccount(domain, localPart, password, localUser); err != nil {
				return err
			}

			addr := localPart + "@" + domain
			cmd.PrintErrf("[account] added %s\n\n", addr)
			printCreds(cmd.OutOrStdout(), cfg.Hostname, addr, password)
			return reportLive(cmd, sasl.AddUser(domain, localPart, password))
		},
	}
	cmd.Flags().String("password", "", "the account's password (generated when omitted)")
	cmd.Flags().String("local-user", "", "deliver mail for this address to /var/mail/<user>; omit for send-only")
	return cmd
}

func accountDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <domain> <local-part>",
		Short: "Remove an account and revoke its login",
		Long: "The mailbox under /var/mail is deliberately kept — mail that was delivered\n" +
			"is data, and withdrawing a login should not destroy it. Remove the\n" +
			"directory by hand if you want the stored mail gone too.",
		Args:    cobra.ExactArgs(2),
		Example: "  docker exec " + app.Name + " " + app.Name + " account delete example.com noreply",
		RunE: func(cmd *cobra.Command, args []string) error {
			domain, localPart := args[0], args[1]
			if err := config.DeleteAccount(domain, localPart); err != nil {
				return err
			}
			cmd.PrintErrf("[account] removed %s@%s (mailbox under /var/mail kept)\n", localPart, domain)
			return reportLive(cmd, sasl.DeleteUser(domain, localPart))
		},
	}
}

func accountListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the accounts, with where their mail goes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			type row struct {
				Address   string `json:"address"`
				Domain    string `json:"domain"`
				LocalPart string `json:"local_part"`
				LocalUser string `json:"local_user,omitempty"`
				Mailbox   string `json:"mailbox,omitempty"`
				SendOnly  bool   `json:"send_only"`
				Relay     string `json:"relay,omitempty"`
			}
			// Built even for the table, so the two outputs cannot disagree about
			// what an account is.
			rows := make([]row, 0)
			for _, d := range cfg.Domains {
				for _, acc := range d.Accounts {
					r := row{
						Address:   accountAddr(d.Name, acc),
						Domain:    d.Name,
						LocalPart: acc.LocalPart,
						LocalUser: acc.LocalUser,
						SendOnly:  acc.LocalUser == "",
					}
					if acc.LocalUser != "" {
						r.Mailbox = "/var/mail/" + acc.LocalUser
					}
					if rl := relayFor(cfg, d, acc); rl != nil {
						r.Relay = rl.Host + ":" + strconv.Itoa(rl.EffectivePort())
					}
					rows = append(rows, r)
				}
			}

			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				// An empty list is [] rather than null, so a consumer can iterate
				// it without checking first.
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}

			if len(rows) == 0 {
				cmd.PrintErrln("no accounts in " + config.Path)
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defer w.Flush()
			fmt.Fprintln(w, "ADDRESS\tDELIVERY\tRELAY")
			for _, r := range rows {
				delivery := r.Mailbox
				if r.SendOnly {
					delivery = "send-only"
				}
				relay := r.Relay
				if relay == "" {
					relay = "direct"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", r.Address, delivery, relay)
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "print the list as JSON")
	return cmd
}

// reportLive turns the result of a live SASL change into the rest of the work
// and a clear message, without failing the command.
//
// The config file is already written by the time this runs, so the account
// change has happened; what may not have happened is applying it to a running
// server. Outside the container that always fails, and it is not an error —
// the next start provisions everything from config.yml anyway.
func reportLive(cmd *cobra.Command, saslErr error) error {
	if err := applyLive(saslErr); err != nil {
		cmd.PrintErrf("\n[account] could not apply to the running server: %v\n", err)
		cmd.PrintErrln("[account] restart mailio to apply (docker compose restart mailio).")
	}
	return nil
}

// applyLive reconciles the running server with the config that was just edited:
// the SASL login, then the routing maps, then a reload. Every step only works
// inside the running container.
func applyLive(saslErr error) error {
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
