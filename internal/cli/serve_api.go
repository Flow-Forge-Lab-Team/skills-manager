package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/skills/")
	if name == "" || strings.Contains(name, "/") {
		http.Error(w, "skill name required", http.StatusBadRequest)
		return
	}
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
		updates = []pendingUpdateView{}
	}
	writeJSONResponse(w, updates)
}

func (s *serveServer) handleUpdateSub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/updates/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[1] != "diff" {
		http.Error(w, "expected /api/v1/updates/<skill>/diff", http.StatusBadRequest)
		return
	}
	diff, err := loadTriageUpdateDiff(s.home, parts[0])
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
	if r.Header.Get("X-Skills-Manager-Token") != s.token {
		writeAPIError(w, http.StatusForbidden, fmt.Errorf("invalid or missing session token"))
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
