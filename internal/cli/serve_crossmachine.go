package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type triageMachine struct {
	Name       string `json:"name"`
	LastSynced string `json:"last_synced,omitempty"`
	LastCommit string `json:"last_commit,omitempty"`
	Current    bool   `json:"current"`
	Drift      string `json:"drift"` // in-sync | diverged | unknown
}

type triageMissingLocked struct {
	Project string   `json:"project"`
	Skills  []string `json:"skills"`
}

type triageCrossMachine struct {
	Library             string                `json:"library"`
	IsGitRepo           bool                  `json:"is_git_repo"`
	HasRemote           bool                  `json:"has_remote"`
	CurrentMachine      string                `json:"current_machine"`
	HeadCommit          string                `json:"head_commit,omitempty"`
	GitStatus           string                `json:"git_status,omitempty"`
	Machines            []triageMachine       `json:"machines"`
	ProjectOverlap      []triageProject       `json:"project_overlap"`
	MissingLockedSkills []triageMissingLocked `json:"missing_locked_skills"`
}

func loadTriageCrossMachine(home string) (triageCrossMachine, error) {
	library := filepath.Join(home, "library")
	out := triageCrossMachine{
		Library:             library,
		CurrentMachine:      currentMachineName(),
		Machines:            []triageMachine{},
		ProjectOverlap:      []triageProject{},
		MissingLockedSkills: []triageMissingLocked{},
	}

	if _, err := os.Stat(filepath.Join(library, ".git")); err == nil {
		out.IsGitRepo = true
		out.HasRemote = hasGitRemote(library)
		if head, err := runGit(library, "rev-parse", "--short", "HEAD"); err == nil {
			out.HeadCommit = strings.TrimSpace(head)
		}
		if status, err := runGit(library, "status", "--short", "--branch"); err == nil {
			out.GitStatus = strings.TrimSpace(status)
		}
	}

	machines, err := readMachines(filepath.Join(library, ".machines.yaml"))
	if err == nil {
		// Drift is measured against what *this* machine last synced to, not the
		// literal HEAD. sync-library records each machine's last_commit and then
		// makes a follow-up "Update machine sync status" commit, so HEAD is one
		// commit ahead of every recorded last_commit — comparing to HEAD would
		// mark even the just-synced current machine as diverged.
		baseline := out.HeadCommit
		if cur, ok := machines.Machines[out.CurrentMachine]; ok && cur.LastCommit != "" {
			baseline = cur.LastCommit
		}
		names := make([]string, 0, len(machines.Machines))
		for name := range machines.Machines {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			entry := machines.Machines[name]
			out.Machines = append(out.Machines, triageMachine{
				Name:       name,
				LastSynced: entry.LastSynced,
				LastCommit: entry.LastCommit,
				Current:    name == out.CurrentMachine,
				Drift:      machineDrift(entry.LastCommit, baseline),
			})
		}
	}

	// Project overlap: which projects have which skills installed on this
	// machine. Cross-machine project membership is not part of the v0.2 model,
	// so this reflects the local machine's manifests.
	projects, err := loadTriageProjects(home)
	if err != nil {
		return out, err
	}
	out.ProjectOverlap = projects

	// Missing locked skills: a project's installed.lock references a skill the
	// local library no longer provides (e.g. before a pull). Remediation is to
	// sync the library or reinstall.
	cat, _, err := loadTriageCatalog(home)
	if err != nil {
		return out, err
	}
	inLibrary := map[string]bool{}
	for _, s := range cat.Skills {
		inLibrary[s.Name] = true
	}
	for _, p := range projects {
		var missing []string
		for _, skill := range p.Skills {
			if !inLibrary[skill] {
				missing = append(missing, skill)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			out.MissingLockedSkills = append(out.MissingLockedSkills, triageMissingLocked{Project: p.Slug, Skills: missing})
		}
	}
	return out, nil
}

func machineDrift(machineCommit, headCommit string) string {
	if machineCommit == "" || headCommit == "" {
		return "unknown"
	}
	if machineCommit == headCommit {
		return "in-sync"
	}
	return "diverged"
}

type triageSettings struct {
	Mode                 string `json:"mode"`
	LLMProvider          string `json:"llm_provider"`
	LLMModel             string `json:"llm_model"`
	LLMAPIKeyEnv         string `json:"llm_api_key_env"`
	UpdateFrequencyHours int    `json:"update_frequency_hours"`
	LibraryHasRemote     bool   `json:"library_has_remote"`
}

func loadTriageSettings(home string) (triageSettings, error) {
	cfg, err := loadManagerConfig(home)
	if err != nil {
		return triageSettings{}, err
	}
	return triageSettings{
		Mode:                 cfg.Mode,
		LLMProvider:          cfg.LLM.Provider,
		LLMModel:             cfg.LLM.Model,
		LLMAPIKeyEnv:         cfg.LLM.APIKeyEnv,
		UpdateFrequencyHours: cfg.effectiveUpdateFrequencyHours(),
		LibraryHasRemote:     hasGitRemote(filepath.Join(home, "library")),
	}, nil
}
