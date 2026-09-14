// Command mailio configures and runs a Postfix mail server from one YAML file.
package main

import (
	"os"

	"mailio/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
