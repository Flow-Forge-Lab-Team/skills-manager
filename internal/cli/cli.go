package cli

import (
	"fmt"
	"io"
)

const Version = "0.1.0-dev"

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	gf, rest, err := extractGlobalFlags(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	// --version is a top-level concern, handled before any subcommand dispatch.
	for _, a := range rest {
		if a == "--version" {
			fmt.Fprintf(stdout, "skills-manager %s\n", Version)
			return ExitSuccess
		}
	}

	// Bare `skills-manager` or `--help`: print top-level help.
	if len(rest) == 0 || (gf.Help && len(rest) == 0) {
		fmt.Fprintln(stdout, helpText(""))
		return ExitSuccess
	}

	cmd := rest[0]
	cmdArgs := rest[1:]

	// `help [cmd]` and `--help` on a subcommand.
	if cmd == "help" {
		topic := ""
		if len(cmdArgs) > 0 {
			topic = cmdArgs[0]
		}
		fmt.Fprintln(stdout, helpText(topic))
		return ExitSuccess
	}
	if gf.Help {
		fmt.Fprintln(stdout, helpText(cmd))
		return ExitSuccess
	}

	log := openLogger(gf.Verbose)
	defer log.close()
	log.log("info", cmd, map[string]string{"args": fmt.Sprint(cmdArgs)})

	var code int
	switch cmd {
	case "install":
		code = runInstall(cmdArgs, stdout, stderr, false, gf)
	case "sync":
		code = runInstall(cmdArgs, stdout, stderr, true, gf)
	case "uninstall":
		code = runUninstall(cmdArgs, stdout, stderr, gf)
	case "list":
		code = runList(cmdArgs, stdout, stderr, gf)
	case "show":
		code = runShow(cmdArgs, stdout, stderr, gf)
	case "update":
		code = runUpdate(cmdArgs, stdout, stderr, gf)
	default:
		fmt.Fprintf(stderr, "unknown argument: %s\n", cmd)
		fmt.Fprintln(stderr, "Usage: skills-manager <command>")
		log.log("error", cmd, map[string]string{"reason": "unknown command"})
		return ExitUsageError
	}

	log.log("info", cmd, map[string]string{"exit": fmt.Sprint(code)})
	return code
}
