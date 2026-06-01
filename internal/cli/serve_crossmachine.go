package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type triageMachine struct {
	Name               string `json:"name"`
	LastSynced         string `json:"last_synced,omitempty"`
	LastCommit         string `json:"last_commit,omitempty"`
	Current            bool   `json:"current"`
	Drift              string `json:"drift"` // in-sync | diverged | unknown
	LastScan           string `json:"last_scan,omitempty"`
	ToolsFound         int    `json:"tools_found,omitempty"`
	GlobalSkills       int    `json:"global_skills,omitempty"`
	ProjectLocalSkills int    `json:"project_local_skills,omitempty"`
	InventoryDrift     string `json:"inventory_drift,omitempty"` // in-sync | drifted | unknown
}

type triageMissingLocked struct {
	Project string   `json:"project"`
	Skills  []string `json:"skills"`
}

type triageCrossMachine struct {
	Library             string                   `json:"library"`
	IsGitRepo           bool                     `json:"is_git_repo"`
	HasRemote           bool                     `json:"has_remote"`
	CurrentMachine      string                   `json:"current_machine"`
	HeadCommit          string                   `json:"head_commit,omitempty"`
	GitStatus           string                   `json:"git_status,omitempty"`
	Machines            []triageMachine          `json:"machines"`
	InventoryFindings   []triageInventoryFinding `json:"inventory_findings"`
	ProjectOverlap      []triageProject          `json:"project_overlap"`
	MissingLockedSkills []triageMissingLocked    `json:"missing_locked_skills"`
}

type triageInventoryFinding struct {
	SkillName string   `json:"skill_name"`
	Status    string   `json:"status"`
	Machines  []string `json:"machines"`
	Detail    string   `json:"detail"`
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
				Name:               name,
				LastSynced:         entry.LastSynced,
				LastCommit:         entry.LastCommit,
				Current:            name == out.CurrentMachine,
				Drift:              machineDrift(entry.LastCommit, baseline),
				LastScan:           entry.Inventory.LastScan,
				ToolsFound:         entry.Inventory.ToolsFound,
				GlobalSkills:       entry.Inventory.GlobalSkills,
				ProjectLocalSkills: entry.Inventory.ProjectLocalSkills,
				InventoryDrift:     machineInventoryDrift(entry.Inventory.InventoryDigest, machines.Machines[out.CurrentMachine].Inventory.InventoryDigest),
			})
		}
		snapshots := loadMachineInventorySnapshots(library, machines)
		out.InventoryFindings = compareMachineInventorySnapshots(snapshots)
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

func machineInventoryDrift(machineDigest, currentDigest string) string {
	if machineDigest == "" || currentDigest == "" {
		return "unknown"
	}
	if machineDigest == currentDigest {
		return "in-sync"
	}
	return "drifted"
}

func loadMachineInventorySnapshots(library string, machines machinesFile) []machineInventorySnapshot {
	var out []machineInventorySnapshot
	for name, entry := range machines.Machines {
		if entry.Inventory.SnapshotPath == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(library, filepath.FromSlash(entry.Inventory.SnapshotPath)))
		if err != nil {
			continue
		}
		var snapshot machineInventorySnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			continue
		}
		if snapshot.Machine == "" {
			snapshot.Machine = name
		}
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Machine < out[j].Machine })
	return out
}

func compareMachineInventorySnapshots(snapshots []machineInventorySnapshot) []triageInventoryFinding {
	if len(snapshots) < 2 {
		return []triageInventoryFinding{}
	}
	machines := make([]string, 0, len(snapshots))
	bySkillMachine := map[string]map[string][]machineInventoryInstall{}
	for _, snapshot := range snapshots {
		machines = append(machines, snapshot.Machine)
		for _, inst := range snapshot.Installations {
			if bySkillMachine[inst.SkillName] == nil {
				bySkillMachine[inst.SkillName] = map[string][]machineInventoryInstall{}
			}
			bySkillMachine[inst.SkillName][snapshot.Machine] = append(bySkillMachine[inst.SkillName][snapshot.Machine], inst)
		}
	}
	sort.Strings(machines)
	var findings []triageInventoryFinding
	skills := make([]string, 0, len(bySkillMachine))
	for skill := range bySkillMachine {
		skills = append(skills, skill)
	}
	sort.Strings(skills)
	for _, skill := range skills {
		perMachine := bySkillMachine[skill]
		var missing []string
		hashes := map[string]bool{}
		scopes := map[string]bool{}
		present := []string{}
		for _, machine := range machines {
			installs := perMachine[machine]
			if len(installs) == 0 {
				missing = append(missing, machine)
				continue
			}
			present = append(present, machine)
			for _, inst := range installs {
				if inst.ContentSHA256 != "" {
					hashes[inst.ContentSHA256] = true
				}
				scopes[inst.Scope] = true
			}
		}
		if len(missing) > 0 {
			findings = append(findings, triageInventoryFinding{SkillName: skill, Status: "missing", Machines: append([]string{}, missing...), Detail: "missing from " + strings.Join(missing, ", ")})
		}
		if len(hashes) > 1 {
			findings = append(findings, triageInventoryFinding{SkillName: skill, Status: "drifted", Machines: append([]string{}, present...), Detail: "same-name hash variants across machines"})
		} else if len(missing) == 0 {
			findings = append(findings, triageInventoryFinding{SkillName: skill, Status: "same", Machines: append([]string{}, present...), Detail: "same content hash on all machines"})
		}
		if len(scopes) > 1 {
			findings = append(findings, triageInventoryFinding{SkillName: skill, Status: "scope-diff", Machines: append([]string{}, present...), Detail: "global and project-local scope differ across machines"})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Status != findings[j].Status {
			return findings[i].Status < findings[j].Status
		}
		return findings[i].SkillName < findings[j].SkillName
	})
	return findings
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
