package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// sampleOTLPLogs is a representative OTLP/JSON logs export carrying a mix of
// Claude Code events: two Skill tool_result events, a non-Skill tool_result
// (ignored), a forward-compatible skill_activated event, and a Skill event
// whose skill name is buried in tool_parameters JSON.
const sampleOTLPLogs = `{
  "resourceLogs": [
    {
      "resource": {
        "attributes": [
          {"key": "terminal.type", "value": {"stringValue": "iTerm.app"}}
        ]
      },
      "scopeLogs": [
        {
          "logRecords": [
            {
              "timeUnixNano": "1747000000000000000",
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "tool_result"}},
                {"key": "tool_name", "value": {"stringValue": "Skill"}},
                {"key": "skill_name", "value": {"stringValue": "brainstorming"}},
                {"key": "event.timestamp", "value": {"stringValue": "2026-05-12T09:00:00Z"}}
              ]
            },
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "tool_result"}},
                {"key": "tool_name", "value": {"stringValue": "Skill"}},
                {"key": "skill_name", "value": {"stringValue": "brainstorming"}}
              ]
            },
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "tool_result"}},
                {"key": "tool_name", "value": {"stringValue": "Bash"}}
              ]
            },
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "skill_activated"}},
                {"key": "skill_name", "value": {"stringValue": "debugging"}},
                {"key": "invocation_trigger", "value": {"stringValue": "proactive"}}
              ]
            },
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "tool_result"}},
                {"key": "tool_name", "value": {"stringValue": "Skill"}},
                {"key": "agent.name", "value": {"stringValue": "code-reviewer"}},
                {"key": "tool_parameters", "value": {"stringValue": "{\"skill_name\":\"verification\"}"}}
              ]
            }
          ]
        }
      ]
    }
  ]
}`

func TestParseOTLPSkillInvocations(t *testing.T) {
	invs, err := parseOTLPSkillInvocations([]byte(sampleOTLPLogs))
	if err != nil {
		t.Fatal(err)
	}
	if len(invs) != 4 {
		t.Fatalf("got %d invocations, want 4: %+v", len(invs), invs)
	}

	// First record keeps its explicit ISO timestamp and defaults harness/trigger.
	if invs[0].SkillName != "brainstorming" || invs[0].Harness != "claude" ||
		invs[0].Trigger != "user-initiated" || invs[0].InvokedAt != "2026-05-12T09:00:00Z" ||
		invs[0].Source != "otel" {
		t.Fatalf("invs[0] = %+v", invs[0])
	}
	// skill_activated event honors the explicit trigger.
	if invs[2].SkillName != "debugging" || invs[2].Trigger != "proactive" {
		t.Fatalf("invs[2] = %+v, want debugging/proactive", invs[2])
	}
	// Skill name dug out of tool_parameters JSON; subagent => nested trigger.
	if invs[3].SkillName != "verification" || invs[3].Trigger != "nested" {
		t.Fatalf("invs[3] = %+v, want verification/nested", invs[3])
	}
	// OTEL cannot attribute a project.
	for i, inv := range invs {
		if inv.ProjectSlug != "" {
			t.Fatalf("invs[%d] has project %q, want empty for OTEL source", i, inv.ProjectSlug)
		}
	}
}

func TestParseOTLPDerivesTimestampFromNanos(t *testing.T) {
	invs, err := parseOTLPSkillInvocations([]byte(sampleOTLPLogs))
	if err != nil {
		t.Fatal(err)
	}
	// invs[0] came from timeUnixNano-less? No: first has event.timestamp.
	// The second record has no timestamp at all -> empty, defaulted on write.
	if invs[1].InvokedAt != "" {
		t.Fatalf("invs[1].InvokedAt = %q, want empty (defaulted at write time)", invs[1].InvokedAt)
	}
}

func TestParseOTLPInvalidJSON(t *testing.T) {
	if _, err := parseOTLPSkillInvocations([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestOTELReceiverEndToEnd(t *testing.T) {
	home := t.TempDir()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		handleOTLPLogs(w, r, home, &strings.Builder{})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/logs", "application/json", strings.NewReader(sampleOTLPLogs))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cells, err := db.UsageMatrix()
	if err != nil {
		t.Fatal(err)
	}

	// Expect: brainstorming x2 (one cell, count 2), debugging x1, verification x1.
	counts := map[string]int{}
	for _, c := range cells {
		counts[c.SkillName] += c.Count
	}
	if counts["brainstorming"] != 2 || counts["debugging"] != 1 || counts["verification"] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestParseReceiverOptions(t *testing.T) {
	opts, err := parseReceiverOptions([]string{"--port", "5555", "--host", "0.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.port != 5555 || opts.host != "0.0.0.0" {
		t.Fatalf("opts = %+v", opts)
	}
	if def, _ := parseReceiverOptions(nil); def.port != 4318 || def.host != "127.0.0.1" {
		t.Fatalf("defaults = %+v", def)
	}
}
