// Package util holds small helpers shared across the setup packages.
package util

import (
	"fmt"
	"os"
	"os/exec"
)

// ParseInt parses a base-10 integer, returning 0 on failure.
func ParseInt(s string) int {
	var n int
	fmt.Sscan(s, &n)
	return n
}

// Run executes a command, wiring its stdout/stderr to the process's.
func Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
