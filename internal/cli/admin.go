package cli

import (
	"time"

	"github.com/spf13/cobra"

	"mailio/internal/api"
)

func adminCmd() *cobra.Command {
	cmd := group(&cobra.Command{Use: "admin", Short: "Get into the admin interface"})
	cmd.AddCommand(adminResetCmd())
	return cmd
}

// adminResetCmd is how somebody locked out gets back in. The token is readable exactly once, so
// a lost link is reissued rather than recovered.
func adminResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Print a link that makes an admin, or sets an existing admin's password",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, st, _, err := setup()
			if err != nil {
				return err
			}
			defer st.Close()

			token, expires, err := st.CreateAdminLink(cmd.Context())
			if err != nil {
				return err
			}
			cmd.Println(cfg.Link(api.AdminLinkPath(token)))
			cmd.PrintErrf("good until %s, once; any earlier link no longer works\n", expires.Format(time.RFC3339))
			return nil
		},
	}
}
