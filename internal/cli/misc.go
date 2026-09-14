package cli

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mailio/internal/app"
	"mailio/internal/config"
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

// configPathHint names the config file for an error message. It is a function
// rather than the constant inlined so the one place that knows the path stays
// internal/config.
func configPathHint() string { return config.Path }

// healthcheckCmd is what the image's HEALTHCHECK runs, so the container needs no
// mail client of its own and a wedged daemon fails it.
//
// It checks all three daemons, because the interesting failure is a partial one.
// Postfix alone answering proves very little: milter_default_action=accept means
// a dead OpenDKIM is invisible from the outside — mail keeps flowing, unsigned,
// and the first anyone hears of it is recipients filing it as spam.
func healthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Check that Postfix, OpenDKIM and OpenDMARC are answering",
		Long: "Exits non-zero naming the first daemon that does not answer on loopback.\n" +
			"It deliberately does not read config.yml: a healthcheck that needed the\n" +
			"config would report the volume rather than the server.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := smtpBanner("127.0.0.1:25"); err != nil {
				return fmt.Errorf("postfix: %w", err)
			}
			if err := dial("127.0.0.1:8891"); err != nil {
				return fmt.Errorf("opendkim: %w", err)
			}
			if err := dial("127.0.0.1:8893"); err != nil {
				return fmt.Errorf("opendmarc: %w", err)
			}
			cmd.PrintErrln("postfix, opendkim and opendmarc are all answering")
			return nil
		},
	}
}

const healthTimeout = 5 * time.Second

// dial proves something is listening — which is all a milter socket can be asked,
// since the milter protocol has no hello a shell-level check could exchange.
func dial(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, healthTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// smtpBanner goes one step further than a connect: a listening socket with a
// wedged smtpd behind it accepts the connection and then says nothing, which is
// exactly the failure a healthcheck exists to catch.
func smtpBanner(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, healthTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(healthTimeout)); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("no greeting: %w", err)
	}
	if !strings.HasPrefix(line, "220") {
		return fmt.Errorf("greeted with %q", strings.TrimSpace(line))
	}
	// Leaving without QUIT logs a "lost connection" line on every check.
	fmt.Fprint(conn, "QUIT\r\n")
	return nil
}
