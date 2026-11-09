// Command mailio is a mail server that keeps its mail in one SQLite file.
package main

import (
	"os"
	"time"

	"mailio/internal/cli"
)

func main() {
	// Everything stored and logged is UTC.
	time.Local = time.UTC
	os.Exit(cli.Execute())
}
