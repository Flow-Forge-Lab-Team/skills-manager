package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/webui"
)

type serveServer struct {
	home  string
	token string
	runMu sync.Mutex
	mux   *http.ServeMux
}

func newServeServer(home string) *serveServer {
	token, err := randomServeToken()
	if err != nil {
		token = "dev-insecure-token"
	}
	s := &serveServer{home: home, token: token, mux: http.NewServeMux()}
	s.mux.HandleFunc("/api/v1/session", s.handleSession)
	s.mux.HandleFunc("/api/v1/overview", s.handleOverview)
	s.mux.HandleFunc("/api/v1/skills", s.handleSkills)
	s.mux.HandleFunc("/api/v1/skills/", s.handleSkillDetail)
	s.mux.HandleFunc("/api/v1/projects", s.handleProjects)
	s.mux.HandleFunc("/api/v1/projects/", s.handleProjectDetail)
	s.mux.HandleFunc("/api/v1/updates", s.handleUpdates)
	s.mux.HandleFunc("/api/v1/updates/", s.handleUpdateSub)
	s.mux.HandleFunc("/api/v1/matrix", s.handleMatrix)
	s.mux.HandleFunc("/api/v1/usage", s.handleUsage)
	s.mux.HandleFunc("/api/v1/run", s.handleRunCLI)
	s.mux.Handle("/", s.handleStatic())
	return s
}

func randomServeToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (s *serveServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.mux.ServeHTTP(w, r)
}

func (s *serveServer) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSONResponse(w, map[string]string{
		"token": s.token,
		"home":  s.home,
	})
}

func (s *serveServer) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	overview, err := loadTriageOverview(s.home)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	overview.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	writeJSONResponse(w, overview)
}

func (s *serveServer) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	skills, err := loadTriageSkillList(s.home, r.URL.Query())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, skills)
}

func (s *serveServer) handleSkillDetail(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/skills/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[1] == "compatibility" {
		s.handleSkillCompatibility(w, r, parts[0])
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		http.Error(w, "skill name required", http.StatusBadRequest)
		return
	}
	name := parts[0]
	switch r.Method {
	case http.MethodGet:
		detail, err := loadTriageSkillDetail(s.home, name)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeAPIError(w, http.StatusNotFound, err)
				return
			}
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSONResponse(w, detail)
	case http.MethodPatch:
		if !s.authorizeAPIWrite(w, r) {
			return
		}
		var req triageSkillMetadataUpdate
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}
		if err := updateTriageSkillMetadata(s.home, name, req); err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeAPIError(w, http.StatusNotFound, err)
				return
			}
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		detail, err := loadTriageSkillDetail(s.home, name)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSONResponse(w, detail)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func (s *serveServer) handleSkillCompatibility(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := validateSkillName(name); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if !s.authorizeAPIWrite(w, r) {
		return
	}
	var req triageSkillCompatibilityUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if req.Mode == "" {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("mode required"))
		return
	}
	args := []string{"--json", "--non-interactive", "--quiet", "set", name, "--compatibility", req.Mode}
	switch req.Mode {
	case "portable":
	case "exclusive":
		if req.Harness == "" {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("harness required for exclusive mode"))
			return
		}
		args = append(args, "--harness", req.Harness)
	case "compatible":
		if len(req.Harnesses) == 0 {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("harnesses required for compatible mode"))
			return
		}
		args = append(args, "--harnesses", strings.Join(req.Harnesses, ","))
	default:
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid compatibility mode: %s", req.Mode))
		return
	}
	if req.Reason != "" {
		args = append(args, "--reason", req.Reason)
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	code := s.runCLIInHome(args, &stdoutBuf, &stderrBuf)
	if code != ExitSuccess {
		writeAPIError(w, http.StatusBadRequest, errors.New(strings.TrimSpace(stderrBuf.String())))
		return
	}
	detail, err := loadTriageSkillDetail(s.home, name)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, detail)
}

func (s *serveServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	projects, err := loadTriageProjects(s.home)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, projects)
}

func (s *serveServer) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimPrefix(r.URL.Path, "/api/v1/projects/")
	if slug == "" || strings.Contains(slug, "/") {
		http.Error(w, "project slug required", http.StatusBadRequest)
		return
	}
	detail, err := loadTriageProjectDetail(s.home, slug)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeAPIError(w, http.StatusNotFound, err)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, detail)
}

func (s *serveServer) handleUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	updates, err := loadTriageUpdates(s.home)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	if updates == nil {
		updates = []triageUpdateView{}
	}
	writeJSONResponse(w, updates)
}

func (s *serveServer) handleUpdateSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/updates/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.Error(w, "expected /api/v1/updates/<skill>/<diff|accept|reject|pin|unpin|summary>", http.StatusBadRequest)
		return
	}
	skill, action := parts[0], parts[1]

	if action == "diff" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		diff, err := loadTriageUpdateDiff(s.home, skill)
		if err != nil {
			if strings.Contains(err.Error(), "no pending") {
				writeAPIError(w, http.StatusNotFound, err)
				return
			}
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(diff))
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := validateSkillName(skill); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if !s.authorizeAPIWrite(w, r) {
		return
	}

	var args []string
	switch action {
	case "accept":
		args = []string{"update", "--accept", skill}
	case "reject":
		args = []string{"update", "--reject", skill}
	case "unpin":
		args = []string{"update", "--unpin", skill}
	case "pin":
		var body struct {
			Version string `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		args = []string{"update", "--pin", skill}
		if strings.TrimSpace(body.Version) != "" {
			args = append(args, strings.TrimSpace(body.Version))
		}
	case "summary":
		var body struct {
			Mode string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mode := "--auto"
		if body.Mode == "handoff" {
			mode = "--handoff"
		}
		args = []string{"summarize", skill, mode}
	default:
		http.Error(w, "unknown update action: "+action, http.StatusBadRequest)
		return
	}

	fullArgs := append([]string{"--json", "--non-interactive", "--quiet"}, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	code := s.runCLIInHome(fullArgs, &stdoutBuf, &stderrBuf)
	// Always return 200 with the exit code in the body (matching /api/v1/run).
	// A non-zero exit here is an expected, recoverable outcome — e.g. the
	// divergence guard refusing an accept, or `summarize --auto` finding no
	// configured provider so the client can offer the handoff fallback. The
	// client inspects exit_code/stderr rather than treating it as a transport
	// error.
	writeJSONResponse(w, cliRunResponse{
		ExitCode: code,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
	})
}

func (s *serveServer) handleMatrix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	matrix, err := loadTriageMatrix(s.home)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, matrix)
}

func (s *serveServer) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := loadUsageMatrix(s.home)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, view)
}

var serveAllowedCLI = map[string]bool{
	"status": true,
	"list":   true,
	"check":  true,
	"update": true,
	"doctor": true,
	"scan":   true,
	"match":  true,
}

type cliRunRequest struct {
	Args []string `json:"args"`
}

type cliRunResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func (s *serveServer) handleRunCLI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeAPIWrite(w, r) {
		return
	}
	var req cliRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if len(req.Args) == 0 {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("args required"))
		return
	}
	cmd := req.Args[0]
	if !serveAllowedCLI[cmd] {
		writeAPIError(w, http.StatusForbidden, fmt.Errorf("command %q is not allowed from the UI", cmd))
		return
	}

	args := append([]string{"--json", "--non-interactive", "--quiet"}, req.Args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	code := s.runCLIInHome(args, &stdoutBuf, &stderrBuf)
	writeJSONResponse(w, cliRunResponse{
		ExitCode: code,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
	})
}

func (s *serveServer) authorizeAPIWrite(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Skills-Manager-Token") != s.token {
		writeAPIError(w, http.StatusForbidden, fmt.Errorf("invalid or missing session token"))
		return false
	}
	return true
}

type triageSkillMetadataUpdate struct {
	Categories   *[]string     `json:"categories,omitempty"`
	Tags         *[]string     `json:"tags,omitempty"`
	Requirements *requirements `json:"requirements,omitempty"`
}

type triageSkillCompatibilityUpdate struct {
	Mode      string   `json:"mode"`
	Harness   string   `json:"harness,omitempty"`
	Harnesses []string `json:"harnesses,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

func (s *serveServer) runCLIInHome(args []string, stdout, stderr io.Writer) int {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	prevHome := os.Getenv("SKILLS_MANAGER_HOME")
	os.Setenv("SKILLS_MANAGER_HOME", s.home)
	code := Run(args, stdout, stderr)
	if prevHome == "" {
		os.Unsetenv("SKILLS_MANAGER_HOME")
	} else {
		os.Setenv("SKILLS_MANAGER_HOME", prevHome)
	}
	return code
}

func (s *serveServer) handleStatic() http.Handler {
	dist, _ := fs.Sub(webui.Dist, "dist")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveStaticAsset(w, r, dist)
	})
}

func serveStaticAsset(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if clean == "" || clean == "." {
		clean = "index.html"
	}
	if _, err := fs.Stat(dist, clean); err != nil {
		clean = "index.html"
	}
	data, err := fs.ReadFile(dist, clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch path.Ext(clean) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writeJSONResponse(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
