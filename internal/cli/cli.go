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
	case "check":
		code = runCheck(cmdArgs, stdout, stderr, gf)
	case "compat-check":
		code = runCompatCheck(cmdArgs, stdout, stderr, gf)
	case "install":
		code = runInstall(cmdArgs, stdout, stderr, false, gf)
	case "sync":
		code = runInstall(cmdArgs, stdout, stderr, true, gf)
	case "uninstall":
		code = runUninstall(cmdArgs, stdout, stderr, gf)
	case "list":
		code = runList(cmdArgs, stdout, stderr, gf)
	case "match":
		code = runMatch(cmdArgs, stdout, stderr, gf)
	case "show":
		code = runShow(cmdArgs, stdout, stderr, gf)
	case "update":
		code = runUpdate(cmdArgs, stdout, stderr, gf)
	case "summarize":
		code = runSummarize(cmdArgs, stdout, stderr, gf)
	case "config":
		code = runConfig(cmdArgs, stdout, stderr, gf)
	case "seed-catalog":
		code = runSeedCatalog(cmdArgs, stdout, stderr, gf)
	case "status":
		code = runStatus(cmdArgs, stdout, stderr, gf)
	case "doctor":
		code = runDoctor(cmdArgs, stdout, stderr, gf)
	case "set":
		code = runSet(cmdArgs, stdout, stderr, gf)
	case "add":
		code = runAdd(cmdArgs, stdout, stderr, gf)
	case "scan":
		code = runScan(cmdArgs, stdout, stderr, gf)
	case "new":
		code = runNew(cmdArgs, stdout, stderr, gf)
	case "init":
		code = runInit(cmdArgs, stdout, stderr, gf)
	case "init-library":
		code = runInitLibrary(cmdArgs, stdout, stderr, gf)
	case "join":
		code = runJoin(cmdArgs, stdout, stderr, gf)
	case "sync-library":
		code = runSyncLibrary(cmdArgs, stdout, stderr, gf)
	case "machines":
		code = runMachines(cmdArgs, stdout, stderr, gf)
	case "setup-schedule":
		code = runSetupSchedule(cmdArgs, stdout, stderr, gf)
	case "unschedule":
		code = runUnschedule(cmdArgs, stdout, stderr, gf)
	case "serve":
		code = runServe(cmdArgs, stdout, stderr, gf)
	case "usage":
		code = runUsage(cmdArgs, stdout, stderr, gf)
	case "watch":
		code = runWatch(cmdArgs, stdout, stderr, gf)
	case "assemble":
		code = runAssemble(cmdArgs, stdout, stderr, gf)
	case "compile":
		code = runCompile(cmdArgs, stdout, stderr, gf)
	case "variants":
		code = runVariants(cmdArgs, stdout, stderr, gf)
	case "port":
		code = runPort(cmdArgs, stdout, stderr, gf)
	default:
		fmt.Fprintf(stderr, "unknown argument: %s\n", cmd)
		fmt.Fprintln(stderr, "Usage: skills-manager <command>")
		log.log("error", cmd, map[string]string{"reason": "unknown command"})
		return ExitUsageError
	}

	log.log("info", cmd, map[string]string{"exit": fmt.Sprint(code)})
	return code
}
