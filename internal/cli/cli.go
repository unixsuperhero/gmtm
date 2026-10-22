// Package cli parses arguments, prints human-readable help, dispatches
// built-in subcommands first and external `<bin>-<sub>` executables last.
package cli

import "io"

// Main is the process entry. Implemented by the CLI module.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	_, _ = stdout.Write([]byte("mtm: not yet implemented\n"))
	return 2
}
