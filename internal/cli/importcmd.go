package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"mailio/internal/maildir"
	"mailio/internal/store"
)

func importCmd() *cobra.Command {
	cmd := group(&cobra.Command{Use: "import", Short: "Bring mail in from elsewhere"})
	cmd.AddCommand(importMaildirCmd())
	return cmd
}

// importMaildirCmd reads the Postfix-based mailio's mail. Several addresses share one Maildir
// there when they were delivered to one system user, so it takes several accounts and sorts each
// message to the one it was sent to.
func importMaildirCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "maildir <dir> <address>...",
		Short: "Import a Maildir into accounts' mailboxes, sorted by who each message was sent to",
		Example: "  docker exec mailio mailio import maildir /import/root inbox@reey.ru inbox@vyrtsev.com\n" +
			"  docker exec mailio mailio import maildir --dry-run /import/root inbox@reey.ru",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, st, _, err := setup()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			all, err := st.Accounts(ctx)
			if err != nil {
				return err
			}
			var accounts []*store.Account
			for _, addr := range args[1:] {
				var found *store.Account
				for i := range all {
					if all[i].Address() == addr {
						found = &all[i]
					}
				}
				if found == nil {
					return fmt.Errorf("%s is not an account: add it in the admin interface first", addr)
				}
				accounts = append(accounts, found)
			}
			res, err := maildir.Import(ctx, st, args[0], accounts, dry)
			if err != nil {
				return err
			}
			keys := make([]string, 0, len(res))
			for k := range res {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				cmd.Printf("%6d  %s\n", res[k], k)
			}
			if dry {
				cmd.PrintErrln("dry run: nothing was written")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "count what would be imported, and write nothing")
	return cmd
}
