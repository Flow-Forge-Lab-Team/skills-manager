package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// watchNotification is one detection written to ~/.skills-manager/notifications/.
type watchNotification struct {
	Type         string `json:"type"` // ingest-candidate | drift | user-edit
	Skill        string `json:"skill"`
	Path         string `json:"path"`
	Fingerprint  string `json:"fingerprint"`
	DetectedAt   string `json:"detected_at"`
	AutoIngested bool   `json:"auto_ingested,omitempty"`
	Note         string `json:"note,omitempty"`
}

type watchOptions struct {
	intervalSeconds int
	daemon          bool
	stop            bool
	autoIngest      bool
	pathsOverride   string
	once            bool // run a single poll then return (testing / scripted use)
}

func runWatch(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseWatchOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	if opts.stop {
		return stopWatchDaemon(home, gf.outWriter(stdout), stderr)
	}
	if opts.daemon {
		return startWatchDaemon(home, args, gf.outWriter(stdout), stderr)
	}
	return runWatchLoop(opts, home, gf, stdout, stderr)
}

func parseWatchOptions(args []string) (watchOptions, error) {
	opts := watchOptions{intervalSeconds: 5}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--daemon":
			opts.daemon = true
		case arg == "--stop":
			opts.stop = true
		case arg == "--auto-ingest":
			opts.autoIngest = true
		case arg == "--once":
			opts.once = true
		case arg == "--interval" || arg == "-i":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--interval requires a value (seconds)")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				return opts, fmt.Errorf("--interval must be a positive integer (seconds)")
			}
			opts.intervalSeconds = n
		case strings.HasPrefix(arg, "--interval="):
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--interval="))
			if err != nil || n < 1 {
				return opts, fmt.Errorf("--interval must be a positive integer (seconds)")
			}
			opts.intervalSeconds = n
		case strings.HasPrefix(arg, "--paths="):
			opts.pathsOverride = strings.TrimPrefix(arg, "--paths=")
		case arg == "--paths":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--paths requires a value")
			}
			i++
			opts.pathsOverride = args[i]
		default:
			return opts, fmt.Errorf("unknown watch option: %s", arg)
		}
	}
	if opts.daemon && opts.stop {
		return opts, fmt.Errorf("choose either --daemon or --stop, not both")
	}
	return opts, nil
}

func watchPIDPath(home string) string { return filepath.Join(home, "watch.pid") }

// startWatchDaemon launches the watcher as a detached background process and
// records its PID. The child runs the same command without --daemon.
func startWatchDaemon(home string, args []string, stdout, stderr io.Writer) int {
	if !daemonSupported() {
		fmt.Fprintln(stderr, "daemon mode is not supported on this platform; run `skills-manager watch` in the foreground")
		return ExitUsageError
	}
	if pid, running := readWatchPID(home); running {
		fmt.Fprintf(stderr, "watcher already running (pid %d); use --stop first\n", pid)
		return ExitOpError
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "locate executable: %v\n", err)
		return ExitOpError
	}
	childArgs := []string{"watch"}
	for _, a := range args {
		if a != "--daemon" {
			childArgs = append(childArgs, a)
		}
	}
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "create log dir: %v\n", err)
		return ExitOpError
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "watch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "open log: %v\n", err)
		return ExitOpError
	}
	defer logFile.Close()

	cmd := exec.Command(exe, childArgs...)
	cmd.Env = append(os.Environ(), "SKILLS_MANAGER_HOME="+home)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachSysProcAttr() // platform-specific (see watch_unix.go / watch_windows.go)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "start watcher: %v\n", err)
		return ExitOpError
	}
	pid := cmd.Process.Pid // capture before Release(), which invalidates Pid
	if err := os.WriteFile(watchPIDPath(home), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		fmt.Fprintf(stderr, "write pid file: %v\n", err)
		return ExitOpError
	}
	_ = cmd.Process.Release()
	fmt.Fprintf(stdout, "Watcher started (pid %d). Logs: %s\n", pid, filepath.Join(logDir, "watch.log"))
	fmt.Fprintln(stdout, "Stop with: skills-manager watch --stop")
	return ExitSuccess
}

func stopWatchDaemon(home string, stdout, stderr io.Writer) int {
	if !daemonSupported() {
		fmt.Fprintln(stderr, "daemon mode is not supported on this platform")
		return ExitUsageError
	}
	pid, running := readWatchPID(home)
	if !running {
		fmt.Fprintln(stdout, "No watcher running.")
		_ = os.Remove(watchPIDPath(home))
		return ExitSuccess
	}
	if err := stopProcess(pid); err != nil {
		fmt.Fprintf(stderr, "stop watcher %d: %v\n", pid, err)
		return ExitOpError
	}
	_ = os.Remove(watchPIDPath(home))
	fmt.Fprintf(stdout, "Stopped watcher (pid %d).\n", pid)
	return ExitSuccess
}

// readWatchPID returns the recorded PID and whether that process is alive.
func readWatchPID(home string) (int, bool) {
	data, err := os.ReadFile(watchPIDPath(home))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, processAlive(pid)
}

func runWatchLoop(opts watchOptions, home string, gf globalFlags, stdout, stderr io.Writer) int {
	out := gf.outWriter(stdout)
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return ExitOpError
	}

	poll := func() {
		notes := detectWatchEvents(home, libraryPath, opts.pathsOverride)
		for _, n := range notes {
			if opts.autoIngest && n.Type == "ingest-candidate" {
				n = autoIngestCandidate(n, home, stderr)
			}
			if err := writeWatchNotification(home, n); err != nil {
				fmt.Fprintf(stderr, "write notification: %v\n", err)
				continue
			}
			fmt.Fprintf(out, "%s: %s (%s)%s\n", n.Type, n.Skill, n.Path, ternaryNote(n.AutoIngested))
		}
	}

	if opts.once {
		poll()
		return ExitSuccess
	}

	fmt.Fprintf(out, "Watching for new skills every %ds. Ctrl-C to stop.\n", opts.intervalSeconds)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(time.Duration(opts.intervalSeconds) * time.Second)
	defer ticker.Stop()
	poll() // immediate first pass
	for {
		select {
		case <-sigCh:
			fmt.Fprintln(out, "\nWatcher stopped.")
			return ExitSuccess
		case <-ticker.C:
			poll()
		}
	}
}

func ternaryNote(autoIngested bool) string {
	if autoIngested {
		return " [auto-ingested]"
	}
	return ""
}

// detectWatchEvents scans watched harness paths and classifies each skill that
// is not already represented (by fingerprint) in the library. Only SKILL.md
// files are considered, so memory/cache files are ignored. Duplicate events
// for the same fingerprint collapse because notifications are keyed by it.
func detectWatchEvents(home, libraryPath, pathsOverride string) []watchNotification {
	searchPaths := watchSearchPaths(home, pathsOverride)
	fingerprintIndex := buildFingerprintIndex(libraryPath)
	ignoreList, _ := loadScanIgnore(home)
	managerDirs := managerCreatedSkillDirs(home)

	var notes []watchNotification
	seen := map[string]bool{}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, searchPath := range searchPaths {
		entries, err := os.ReadDir(searchPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillPath := filepath.Join(searchPath, entry.Name())
			if inSlice(ignoreList, skillPath) {
				continue
			}
			skillMd := filepath.Join(skillPath, "SKILL.md")
			fp, _, err := fingerprintSkillMd(skillMd)
			if err != nil {
				continue
			}
			if _, ok := fingerprintIndex[fp]; ok {
				continue // already in library (by content) — no event
			}
			dedupeKey := fp
			if seen[dedupeKey] {
				continue
			}
			seen[dedupeKey] = true

			note := watchNotification{Skill: entry.Name(), Path: skillPath, Fingerprint: fp, DetectedAt: now}
			libMeta, _ := readSkillMeta(filepath.Join(libraryPath, entry.Name(), ".skill-meta.yaml"))
			switch {
			case managerDirs[skillPath]:
				// We created this path; a content change is drift, not ingest.
				note.Type = "drift"
				note.Note = "manager-created skill changed"
			case libMeta.Fingerprint.SHA256 != "":
				// A library skill with this name exists but content differs and
				// we did not create this copy — a user edit, not a new skill.
				note.Type = "user-edit"
				note.Note = "differs from library version"
			default:
				note.Type = "ingest-candidate"
			}
			notes = append(notes, note)
		}
	}
	return notes
}

// watchSearchPaths returns global harness skill dirs plus registered project
// skill dirs (or an explicit override).
func watchSearchPaths(home, pathsOverride string) []string {
	if pathsOverride != "" {
		var out []string
		for _, p := range strings.Split(pathsOverride, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	var paths []string
	if userHome, err := os.UserHomeDir(); err == nil {
		for _, p := range []string{
			filepath.Join(userHome, ".claude", "skills"),
			filepath.Join(userHome, ".codex", "skills"),
			filepath.Join(userHome, ".grok", "skills"),
			filepath.Join(userHome, ".hermes", "skills"),
			filepath.Join(userHome, ".openclaw", "skills"),
			filepath.Join(userHome, ".gemini", "skills"),
			filepath.Join(userHome, ".gemini", "antigravity", "skills"),
		} {
			if _, err := os.Stat(p); err == nil {
				paths = append(paths, p)
			}
		}
	}
	// Registered project skill paths.
	for _, project := range loadProjectPaths(home) {
		for _, rel := range []string{
			filepath.Join(".claude", "skills"),
			filepath.Join(".codex", "skills"),
			filepath.Join(".agents", "skills"),
			"skills",
		} {
			p := filepath.Join(project, rel)
			if _, err := os.Stat(p); err == nil {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// loadProjectPaths returns project directories from registered manifests.
func loadProjectPaths(home string) []string {
	entries, err := os.ReadDir(filepath.Join(home, "manifests"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if m, err := readManifest(filepath.Join(home, "manifests", e.Name())); err == nil && m.ProjectPath != "" {
			out = append(out, m.ProjectPath)
		}
	}
	return out
}

// managerCreatedSkillDirs returns the set of skill directories the manager
// installed, derived from manifest managed paths.
func managerCreatedSkillDirs(home string) map[string]bool {
	dirs := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(home, "manifests"))
	if err != nil {
		return dirs
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		m, err := readManifest(filepath.Join(home, "manifests", e.Name()))
		if err != nil {
			continue
		}
		for _, rel := range m.ManagedPaths {
			abs := filepath.Join(m.ProjectPath, rel)
			// Managed paths point at files or skill dirs; record the skill dir
			// (the directory directly containing SKILL.md).
			if strings.EqualFold(filepath.Base(abs), "SKILL.md") {
				dirs[filepath.Dir(abs)] = true
			} else {
				dirs[abs] = true
			}
		}
	}
	return dirs
}

// autoIngestCandidate attempts a gated, high-confidence auto-ingest. It refuses
// when the SKILL.md contains suspicious instructions; ingestFromSource itself
// refuses anything below high confidence (which also covers unknown
// compatibility / missing-signal cases).
func autoIngestCandidate(n watchNotification, home string, stderr io.Writer) watchNotification {
	if skillMdLooksSuspicious(filepath.Join(n.Path, "SKILL.md")) {
		n.Note = "auto-ingest skipped: suspicious instructions"
		return n
	}
	src := ingestSource{kind: "local", raw: n.Path, path: n.Path, label: n.Path}
	res := ingestFromSource(src, ingestOptions{auto: true, yes: true}, home, io.Discard)
	if res.Skipped {
		n.Note = "auto-ingest skipped: " + res.Reason
		return n
	}
	n.AutoIngested = true
	n.Note = "auto-ingested (confidence high)"
	return n
}

func skillMdLooksSuspicious(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if looksSuspicious(strings.ToLower(strings.TrimSpace(scanner.Text()))) {
			return true
		}
	}
	return false
}

// writeWatchNotification writes one notification, keyed by fingerprint+type so
// repeated detections of the same skill collapse into a single file.
func writeWatchNotification(home string, n watchNotification) error {
	dir := filepath.Join(home, "notifications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fpPrefix := n.Fingerprint
	if len(fpPrefix) > 12 {
		fpPrefix = fpPrefix[:12]
	}
	name := fmt.Sprintf("watch-%s-%s.json", n.Type, fpPrefix)
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), data, 0o644)
}
