package cli

import (
	"fmt"
	"io"
)

const Version = "0.1.0-dev"

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintf(stdout, "skills-manager %s\n", Version)
		return 0
	}

	if len(args) == 0 {
		fmt.Fprintln(stdout, "skills-manager: AI skill library manager")
		fmt.Fprintln(stdout, "Usage: skills-manager --version")
		return 0
	}

	fmt.Fprintf(stderr, "unknown argument: %s\n", args[0])
	fmt.Fprintln(stderr, "Usage: skills-manager --version")
	return 2
}
