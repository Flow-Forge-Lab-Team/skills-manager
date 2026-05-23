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
		fmt.Fprintln(stdout, "Usage: skills-manager <command>")
		return 0
	}

	switch args[0] {
	case "install":
		return runInstall(args[1:], stdout, stderr, false)
	case "sync":
		return runInstall(args[1:], stdout, stderr, true)
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	case "show":
		return runShow(args[1:], stdout, stderr)
	}

	fmt.Fprintf(stderr, "unknown argument: %s\n", args[0])
	fmt.Fprintln(stderr, "Usage: skills-manager <command>")
	return 2
}
