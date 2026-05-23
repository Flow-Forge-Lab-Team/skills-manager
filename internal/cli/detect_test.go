package cli

import (
	"path/filepath"
	"testing"
)

func TestInferRequirements_MCPServer(t *testing.T) {
	detectors := detectorSet{
		compatibilityDetectors: []compatibilityDetector{},
		requirementDetectors: []requirementDetector{
			{
				ID: "linear-connector",
				Requirement: detectorRequirement{
					Kind:     "mcp_server",
					Name:     "linear",
					Required: true,
				},
				Patterns: []string{"mcp__linear__list_issues"},
			},
		},
	}

	skillBody := "mcp__linear__list_issues is a tool for managing Linear issues"
	result := inferRequirements(detectors, skillBody)

	if len(result.MCPServers) != 1 {
		t.Errorf("expected 1 MCP server, got %d", len(result.MCPServers))
	}
	if result.MCPServers[0].Name != "linear" {
		t.Errorf("expected MCP server name 'linear', got %q", result.MCPServers[0].Name)
	}
	if !result.MCPServers[0].Required {
		t.Errorf("expected MCP server to be required")
	}
	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(result.Tools))
	}
}

func TestInferRequirements_Model(t *testing.T) {
	detectors := detectorSet{
		compatibilityDetectors: []compatibilityDetector{},
		requirementDetectors: []requirementDetector{
			{
				ID: "tool-use-required",
				Requirement: detectorRequirement{
					Kind:     "model",
					Name:     "tool_use",
					Required: true,
				},
				Patterns: []string{"use the browser tool"},
			},
		},
	}

	skillBody := "I can use the browser tool to navigate websites"
	result := inferRequirements(detectors, skillBody)

	if result.Model.ToolUse != "required" {
		t.Errorf("expected model.tool_use='required', got %q", result.Model.ToolUse)
	}
	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(result.Tools))
	}
	if len(result.MCPServers) != 0 {
		t.Errorf("expected 0 MCP servers, got %d", len(result.MCPServers))
	}
}

func TestSkillMetaRoundTrip_MCPAndModel(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, ".skill-meta.yaml")

	original := skillMeta{
		Version:    1,
		Categories: []string{"Engineering"},
		Tags:       []string{"test"},
		Summary:    "A test skill",
		Compatibility: compatibility{
			Mode: "portable",
		},
		Requirements: requirements{
			Tools: []toolRequirement{
				{Name: "git", Required: true},
			},
			MCPServers: []mcpRequirement{
				{Name: "linear", Required: true},
			},
			Model: modelRequirement{
				ToolUse: "required",
			},
			Inferred: true,
		},
	}

	if err := writeSkillMeta(metaPath, original); err != nil {
		t.Fatalf("writeSkillMeta failed: %v", err)
	}

	read, err := readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("readSkillMeta failed: %v", err)
	}

	if len(read.Requirements.MCPServers) != 1 {
		t.Errorf("expected 1 MCP server, got %d", len(read.Requirements.MCPServers))
	} else if read.Requirements.MCPServers[0].Name != "linear" {
		t.Errorf("expected MCP server 'linear', got %q", read.Requirements.MCPServers[0].Name)
	}

	if read.Requirements.Model.ToolUse != "required" {
		t.Errorf("expected model.tool_use='required', got %q", read.Requirements.Model.ToolUse)
	}

	if !read.Requirements.Inferred {
		t.Errorf("expected Inferred flag to be true")
	}
}

func TestDetectCompatibility_NamedMCPNotMultiSignal(t *testing.T) {
	detectors := detectorSet{
		compatibilityDetectors: []compatibilityDetector{
			{
				ID:         "claude-patterns",
				Harness:    "claude",
				Confidence: "high",
				Patterns:   []string{"AskUserQuestion", "GetThreadInfo"},
			},
			{
				ID:         "mcp-uuid-local",
				Harness:    "multi",
				Confidence: "medium",
				Patterns:   []string{"mcp__[hex]__"},
			},
		},
		requirementDetectors: []requirementDetector{},
	}

	// Skill with named MCP server reference and Claude pattern
	skillBody := "Call mcp__linear__list_issues and AskUserQuestion"

	// Detect compatibility
	detected := detectCompatibility(detectors, skillBody)

	// Claude should be high-confidence
	claudeResult, hasClaude := detected["claude"]
	if !hasClaude {
		t.Errorf("expected claude detection")
	}
	if claudeResult.Confidence != "high" {
		t.Errorf("expected claude confidence 'high', got %q", claudeResult.Confidence)
	}

	// Multi should NOT be detected (named MCP should not trigger it)
	multiResult, hasMulti := detected["multi"]
	if hasMulti {
		t.Errorf("expected no multi harness signal, got confidence %q", multiResult.Confidence)
	}
}
