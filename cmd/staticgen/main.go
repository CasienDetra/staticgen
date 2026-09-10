// Command staticgen builds a static website from a directory of Markdown files.
package main

import (
	"os"

	"github.com/casien/staticgen/internal/cli"
)

func main() {
	// Cobra reports the error itself, so main only has to set the exit status.
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
