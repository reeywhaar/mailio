package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mailio/internal/app"
	"mailio/internal/letsencrypt"
	"mailio/internal/postfix"
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

func certCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Manage the TLS certificate",
		Long: "With a letsencrypt block in config.yml the certificate is obtained at\n" +
			"startup and renewed by a background loop. Without one the server uses a\n" +
			"self-signed certificate and these commands have nothing to do.",
	}
	cmd.AddCommand(certRenewCmd(), certRenewLoopCmd())
	return group(cmd)
}

func certRenewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "renew",
		Short: "Renew the certificate now, if it is near expiry",
		Long: "Does nothing when the current certificate has more than 30 days left.\n" +
			"Pass --force to renew regardless, which is worth remembering is subject\n" +
			"to Let's Encrypt rate limits.",
		Args:    cobra.NoArgs,
		Example: "  docker exec " + app.Name + " " + app.Name + " cert renew --force",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.LetsEncrypt == nil {
				return fmt.Errorf("letsencrypt is not configured in %s", configPathHint())
			}
			force, _ := cmd.Flags().GetBool("force")
			renewed, err := letsencrypt.Obtain(cfg, postfix.TLSCertFile, postfix.TLSKeyFile, force)
			if err != nil {
				return fmt.Errorf("obtain certificate: %w", err)
			}
			if !renewed {
				cmd.PrintErrln("[acme] certificate is not near expiry; pass --force to renew anyway")
				return nil
			}
			cmd.PrintErrln("[acme] certificate renewed, reloading postfix")
			return util.Run("postfix", "reload")
		},
	}
	cmd.Flags().Bool("force", false, "renew even when the certificate is not near expiry")
	return cmd
}

func certRenewLoopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "renew-loop",
		Short: "Watch the certificate and renew it as it nears expiry",
		Long: "Runs until killed; this is what the entrypoint starts in the background.\n" +
			"It exits immediately when letsencrypt is not configured, so the entrypoint\n" +
			"can start it unconditionally.",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.LetsEncrypt == nil {
				cmd.PrintErrln("[acme] letsencrypt not configured, renewal loop disabled")
				return nil
			}

			cmd.PrintErrf("[acme] renewal loop started (checking every %s, retrying every %s after failure)\n",
				renewInterval, renewRetryInterval)
			interval := renewRetryInterval
			for {
				time.Sleep(interval)
				renewed, err := letsencrypt.Obtain(cfg, postfix.TLSCertFile, postfix.TLSKeyFile, false)
				if err != nil {
					cmd.PrintErrf("[acme] renewal check failed: %v (retrying in %s)\n", err, renewRetryInterval)
					interval = renewRetryInterval
					continue
				}
				interval = renewInterval
				if renewed {
					cmd.PrintErrln("[acme] certificate renewed, reloading postfix")
					if err := util.Run("postfix", "reload"); err != nil {
						cmd.PrintErrf("[acme] postfix reload failed: %v\n", err)
					}
				}
			}
		},
	}
}
