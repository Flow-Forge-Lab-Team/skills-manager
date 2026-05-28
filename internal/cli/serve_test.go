package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
	"gopkg.in/yaml.v3"
)

func TestServeAPIReadsFreshDiskState(t *testing.T) {
	home := t.TempDir()
	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatal(err)
	}

	const skillCount = 250
	const projectCount = 7
	cat := catalog{Version: 1, Skills: make([]catalogSkill, 0, skillCount)}
	for i := 0; i < skillCount; i++ {
		name := "skill-" + strconv.Itoa(i)
		skillDir := filepath.Join(libraryPath, name)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: fixture\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}
		cat.Skills = append(cat.Skills, catalogSkill{
			Name:       name,
			Summary:    "fixture",
			Categories: []string{"Engineering"},
			Tags:       []string{"fixture"},
		})
	}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}

	manifestsDir := filepath.Join(home, "manifests")
	if err := os.MkdirAll(manifestsDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < projectCount; i++ {
		slug := "proj-" + strconv.Itoa(i)
		projectPath := filepath.Join(home, "projects", slug)
		skillsDir := filepath.Join(projectPath, ".skills")
		if err := os.MkdirAll(skillsDir, 0755); err != nil {
			t.Fatal(err)
		}
		lock := installLock{
			Version:     1,
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			GeneratedBy: "test",
			Skills: []installLockEntry{
				{Name: "skill-0", Harnesses: []string{"claude"}},
				{Name: "skill-1", Harnesses: []string{"claude"}},
			},
		}
		lockBytes, _ := yaml.Marshal(lock)
		if err := os.WriteFile(filepath.Join(skillsDir, "installed.lock"), lockBytes, 0644); err != nil {
			t.Fatal(err)
		}
		manifest := installManifest{
			Version:     1,
			ProjectPath: projectPath,
			ProjectSlug: slug,
			InstalledAt: time.Now().UTC().Format(time.RFC3339),
			ManagedPaths: []string{
				".claude/skills/skill-0",
				".claude/skills/skill-1",
			},
		}
		raw, _ := json.Marshal(manifest)
		if err := os.WriteFile(filepath.Join(manifestsDir, slug+".json"), raw, 0644); err != nil {
			t.Fatal(err)
		}
	}

	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	srv := newServeServer(home)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	t.Run("overview counts", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/v1/overview")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
		}
		var overview triageOverview
		if err := json.NewDecoder(resp.Body).Decode(&overview); err != nil {
			t.Fatal(err)
		}
		if overview.LibrarySkills != skillCount {
			t.Fatalf("library_skills = %d, want %d", overview.LibrarySkills, skillCount)
		}
		if overview.Projects != projectCount {
			t.Fatalf("projects = %d, want %d", overview.Projects, projectCount)
		}
	})

	t.Run("matrix under one second", func(t *testing.T) {
		start := time.Now()
		resp, err := http.Get(ts.URL + "/api/v1/matrix")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if time.Since(start) > time.Second {
			t.Fatalf("matrix request took %v, want < 1s", time.Since(start))
		}
		var matrix triageMatrix
		if err := json.NewDecoder(resp.Body).Decode(&matrix); err != nil {
			t.Fatal(err)
		}
		if len(matrix.Skills) != skillCount {
			t.Fatalf("skills = %d, want %d", len(matrix.Skills), skillCount)
		}
		if len(matrix.Projects) != projectCount {
			t.Fatalf("projects = %d, want %d", len(matrix.Projects), projectCount)
		}
	})

	t.Run("no stale cache after catalog write", func(t *testing.T) {
		resp1, err := http.Get(ts.URL + "/api/v1/skills")
		if err != nil {
			t.Fatal(err)
		}
		var skills1 []triageSkill
		json.NewDecoder(resp1.Body).Decode(&skills1)
		resp1.Body.Close()
		if len(skills1) != skillCount {
			t.Fatalf("initial skills = %d", len(skills1))
		}

		extra := filepath.Join(libraryPath, "skill-extra")
		os.MkdirAll(extra, 0755)
		os.WriteFile(filepath.Join(extra, "SKILL.md"), []byte("---\nname: skill-extra\ndescription: new\n---\n"), 0644)
		cat.Skills = append(cat.Skills, catalogSkill{Name: "skill-extra", Summary: "new"})
		writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat)

		resp2, err := http.Get(ts.URL + "/api/v1/skills")
		if err != nil {
			t.Fatal(err)
		}
		var skills2 []triageSkill
		json.NewDecoder(resp2.Body).Decode(&skills2)
		resp2.Body.Close()
		if len(skills2) != skillCount+1 {
			t.Fatalf("after write skills = %d, want %d", len(skills2), skillCount+1)
		}
	})

	t.Run("static index", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})
}

func TestParseServeOptions(t *testing.T) {
	opts, err := parseServeOptions([]string{"--port", "8888", "--host", "0.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.port != 8888 || opts.host != "0.0.0.0" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestServeRunCLIWhitelist(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "library"), 0755)
	srv := newServeServer(home)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body := strings.NewReader(`{"args":["rm","-rf"]}`)
	resp, err := http.Post(ts.URL+"/api/v1/run", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	resp2, err := http.Get(ts.URL + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var sess struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/run", strings.NewReader(`{"args":["status"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Skills-Manager-Token", sess.Token)
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("authorized status run = %d, want 200", resp3.StatusCode)
	}
}

func TestServeBindsLocalhostByDefault(t *testing.T) {
	opts, err := parseServeOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort(opts.host, strconv.Itoa(opts.port))
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("default addr = %s, want 127.0.0.1", addr)
	}
}
