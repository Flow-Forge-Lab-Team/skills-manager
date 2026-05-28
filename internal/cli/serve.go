package cli

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

type serveOptions struct {
	port int
	host string
}

func runServe(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseServeOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	addr := net.JoinHostPort(opts.host, strconv.Itoa(opts.port))
	srv := newServeServer(home)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Fprintf(stdout, "skills-manager triage UI at http://%s\n", addr)
	if opts.host == "127.0.0.1" || opts.host == "localhost" {
		fmt.Fprintln(stdout, "Listening on localhost only. Use --host 0.0.0.0 for Tailscale/LAN access.")
	}

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return ExitOpError
	}
	return ExitSuccess
}

func parseServeOptions(args []string) (serveOptions, error) {
	opts := serveOptions{
		port: 7777,
		host: "127.0.0.1",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--port requires a value")
			}
			p, err := strconv.Atoi(args[i+1])
			if err != nil || p <= 0 || p > 65535 {
				return opts, fmt.Errorf("invalid port: %s", args[i+1])
			}
			opts.port = p
			i++
		case "--host":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--host requires a value")
			}
			opts.host = args[i+1]
			i++
		default:
			return opts, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return opts, nil
}
