package cli

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"mailio/internal/app"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version this binary was built from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println(app.Version)
			return nil
		},
	}
}

// healthcheckCmd is what HEALTHCHECK runs: a second process asking the first, so the image needs
// no HTTP client and a wedged server fails it.
func healthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Ask the running server whether it is well",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get("http://127.0.0.1" + app.ListenAddr + "/healthz")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				return fmt.Errorf("healthz answered %s", resp.Status)
			}
			return nil
		},
	}
}
