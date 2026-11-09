// Package cli is mailio's command line: the daemon, and what an operator needs a shell for.
//
// The first way in is here and nowhere else: an admin exists because somebody with a shell on
// the host opened a link this printed.
package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"mailio/internal/app"
	"mailio/internal/config"
	"mailio/internal/seal"
	"mailio/internal/store"
)

func root() *cobra.Command {
	cmd := &cobra.Command{
		Use:           app.Name,
		Short:         "A mail server that keeps its mail in one SQLite file",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Cobra prints to stderr unless an output is set, which would make `link=$(mailio admin
	// reset)` capture nothing.
	cmd.SetOut(os.Stdout)
	cmd.AddCommand(serveCmd(), adminCmd(), importCmd(), healthcheckCmd(), versionCmd())
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

// group finishes a command that only holds subcommands, so `mailio admin bogus` is refused
// rather than answered with help and exit 0.
func group(cmd *cobra.Command) *cobra.Command {
	cmd.Args = cobra.NoArgs
	cmd.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
	return cmd
}

// setup is the environment, a logger, and the database.
func setup() (*config.Config, *store.Store, *slog.Logger, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	level := new(slog.LevelVar)
	level.Set(cfg.LogLevel)
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level, ReplaceAttr: readable}))
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, nil, nil, err
	}
	sealer, err := seal.New(cfg.SecretKey)
	if err != nil {
		st.Close()
		return nil, nil, nil, err
	}
	st.SetSealer(sealer)
	return cfg, st, log, nil
}

// readable writes a duration as one reads it, "1.2s", rather than as JSON's count of nanoseconds.
func readable(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindDuration {
		return slog.String(a.Key, a.Value.Duration().String())
	}
	return a
}
