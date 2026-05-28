package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// sampleOTLPLogs exercises the three OTEL shapes the receiver must handle:
//   - a skill_activated (user-slash) plus its matching Skill tool_result sharing
//     prompt.id "p1" + skill "brainstorming" → counted once, tool_use_id adopted.
//   - a skill_activated (claude-proactive) for a /-command "debugging" with no
//     tool_result → counted once, no tool_use_id.
//   - a Skill tool_result "verification" with no skill_activated → counted once.
//   - a non-Skill tool_result (Bash) → ignored.
const sampleOTLPLogs = `{
  "resourceLogs": [
    {
      "resource": {"attributes": [{"key": "terminal.type", "value": {"stringValue": "iTerm.app"}}]},
      "scopeLogs": [
        {
          "logRecords": [
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "skill_activated"}},
                {"key": "prompt.id", "value": {"stringValue": "p1"}},
                {"key": "skill.name", "value": {"stringValue": "brainstorming"}},
                {"key": "invocation_trigger", "value": {"stringValue": "user-slash"}},
                {"key": "event.timestamp", "value": {"stringValue": "2026-05-12T09:00:00Z"}}
              ]
            },
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "tool_result"}},
                {"key": "prompt.id", "value": {"stringValue": "p1"}},
                {"key": "tool_name", "value": {"stringValue": "Skill"}},
                {"key": "skill_name", "value": {"stringValue": "brainstorming"}},
                {"key": "tool_use_id", "value": {"stringValue": "toolu_1"}}
              ]
            },
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "skill_activated"}},
                {"key": "prompt.id", "value": {"stringValue": "p2"}},
                {"key": "skill.name", "value": {"stringValue": "debugging"}},
                {"key": "invocation_trigger", "value": {"stringValue": "claude-proactive"}}
              ]
            },
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "tool_result"}},
                {"key": "tool_name", "value": {"stringValue": "Skill"}},
                {"key": "tool_parameters", "value": {"stringValue": "{\"skill_name\":\"verification\"}"}},
                {"key": "tool_use_id", "value": {"stringValue": "toolu_3"}}
              ]
            },
            {
              "attributes": [
                {"key": "event.name", "value": {"stringValue": "tool_result"}},
                {"key": "tool_name", "value": {"stringValue": "Bash"}}
              ]
            }
          ]
        }
      ]
    }
  ]
}`

func findInv(invs []state.Invocation, skill string) (state.Invocation, bool) {
	for _, inv := range invs {
		if inv.SkillName == skill {
			return inv, true
		}
	}
	return state.Invocation{}, false
}

func TestParseOTLPSkillInvocations(t *testing.T) {
	invs, err := parseOTLPSkillInvocations([]byte(sampleOTLPLogs))
	if err != nil {
		t.Fatal(err)
	}
	if len(invs) != 3 {
		t.Fatalf("got %d invocations, want 3: %+v", len(invs), invs)
	}

	// skill_activated bridged to its tool_result: counted once, adopts tool_use_id,
	// maps user-slash -> user-initiated, keeps its timestamp and default harness.
	b, ok := findInv(invs, "brainstorming")
	if !ok || b.ToolUseID != "toolu_1" || b.Trigger != "user-initiated" ||
		b.InvokedAt != "2026-05-12T09:00:00Z" || b.Harness != "claude" || b.Source != "otel" {
		t.Fatalf("brainstorming = %+v", b)
	}
	// /-command: skill_activated only, no tool_use_id, claude-proactive -> proactive.
	d, ok := findInv(invs, "debugging")
	if !ok || d.ToolUseID != "" || d.Trigger != "proactive" {
		t.Fatalf("debugging = %+v", d)
	}
	// tool_result with no skill_activated: still counted, no trigger.
	v, ok := findInv(invs, "verification")
	if !ok || v.ToolUseID != "toolu_3" || v.Trigger != "" {
		t.Fatalf("verification = %+v", v)
	}
	// OTEL cannot attribute a project.
	for _, inv := range invs {
		if inv.ProjectSlug != "" {
			t.Fatalf("inv %q has project %q, want empty for OTEL", inv.SkillName, inv.ProjectSlug)
		}
	}
}

func TestParseOTLPNoDoubleCountForBridgedActivation(t *testing.T) {
	invs, err := parseOTLPSkillInvocations([]byte(sampleOTLPLogs))
	if err != nil {
		t.Fatal(err)
	}
	// brainstorming has both a skill_activated and a tool_result; it must appear once.
	n := 0
	for _, inv := range invs {
		if inv.SkillName == "brainstorming" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("brainstorming appeared %d times, want 1 (bridged, not double-counted)", n)
	}
}

func TestParseOTLPPairsRepeatedSameSkillInPrompt(t *testing.T) {
	// One prompt invokes "brainstorming" twice. Each skill_activated must pair
	// with a distinct tool_result (FIFO), not collapse onto the last tool_use_id.
	const payload = `{"resourceLogs":[{"scopeLogs":[{"logRecords":[
	  {"attributes":[{"key":"event.name","value":{"stringValue":"skill_activated"}},{"key":"prompt.id","value":{"stringValue":"p1"}},{"key":"skill.name","value":{"stringValue":"brainstorming"}},{"key":"invocation_trigger","value":{"stringValue":"user-slash"}}]},
	  {"attributes":[{"key":"event.name","value":{"stringValue":"skill_activated"}},{"key":"prompt.id","value":{"stringValue":"p1"}},{"key":"skill.name","value":{"stringValue":"brainstorming"}},{"key":"invocation_trigger","value":{"stringValue":"claude-proactive"}}]},
	  {"attributes":[{"key":"event.name","value":{"stringValue":"tool_result"}},{"key":"prompt.id","value":{"stringValue":"p1"}},{"key":"tool_name","value":{"stringValue":"Skill"}},{"key":"skill_name","value":{"stringValue":"brainstorming"}},{"key":"tool_use_id","value":{"stringValue":"tA"}}]},
	  {"attributes":[{"key":"event.name","value":{"stringValue":"tool_result"}},{"key":"prompt.id","value":{"stringValue":"p1"}},{"key":"tool_name","value":{"stringValue":"Skill"}},{"key":"skill_name","value":{"stringValue":"brainstorming"}},{"key":"tool_use_id","value":{"stringValue":"tB"}}]}
	]}]}]}`

	invs, err := parseOTLPSkillInvocations([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	// Two activations, paired to the two distinct tool_use_ids; tool_results not
	// re-counted.
	if len(invs) != 2 {
		t.Fatalf("got %d invocations, want 2: %+v", len(invs), invs)
	}
	got := map[string]string{} // tool_use_id -> trigger
	for _, inv := range invs {
		got[inv.ToolUseID] = inv.Trigger
	}
	if got["tA"] != "user-initiated" || got["tB"] != "proactive" {
		t.Fatalf("pairing = %+v, want tA->user-initiated, tB->proactive", got)
	}
}

func TestParseOTLPDoesNotPairStaleEarlierResult(t *testing.T) {
	// A batch boundary places an earlier call's tool_result (seq 10) in the same
	// payload as a later same-skill activation (seq 20) whose own tool_result
	// (seq 21) is also present. The activation must pair with the result that
	// follows it (tB, seq 21), never the stale earlier one (tA, seq 10).
	const payload = `{"resourceLogs":[{"scopeLogs":[{"logRecords":[
	  {"attributes":[{"key":"event.name","value":{"stringValue":"tool_result"}},{"key":"event.sequence","value":{"stringValue":"10"}},{"key":"prompt.id","value":{"stringValue":"p1"}},{"key":"tool_name","value":{"stringValue":"Skill"}},{"key":"skill_name","value":{"stringValue":"brainstorming"}},{"key":"tool_use_id","value":{"stringValue":"tA"}}]},
	  {"attributes":[{"key":"event.name","value":{"stringValue":"skill_activated"}},{"key":"event.sequence","value":{"stringValue":"20"}},{"key":"prompt.id","value":{"stringValue":"p1"}},{"key":"skill.name","value":{"stringValue":"brainstorming"}},{"key":"invocation_trigger","value":{"stringValue":"claude-proactive"}}]},
	  {"attributes":[{"key":"event.name","value":{"stringValue":"tool_result"}},{"key":"event.sequence","value":{"stringValue":"21"}},{"key":"prompt.id","value":{"stringValue":"p1"}},{"key":"tool_name","value":{"stringValue":"Skill"}},{"key":"skill_name","value":{"stringValue":"brainstorming"}},{"key":"tool_use_id","value":{"stringValue":"tB"}}]}
	]}]}]}`

	invs, err := parseOTLPSkillInvocations([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	var activation, standalone state.Invocation
	for _, inv := range invs {
		if inv.Trigger == "proactive" {
			activation = inv
		} else {
			standalone = inv
		}
	}
	if activation.ToolUseID != "tB" {
		t.Fatalf("activation paired with %q, want tB (the result that follows it)", activation.ToolUseID)
	}
	if standalone.ToolUseID != "tA" {
		t.Fatalf("stale earlier result = %q, want tA emitted standalone", standalone.ToolUseID)
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

	counts := map[string]int{}
	for _, c := range cells {
		counts[c.SkillName] += c.Count
	}
	if counts["brainstorming"] != 1 || counts["debugging"] != 1 || counts["verification"] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

// TestUsageEnrichmentBothFeeds verifies the "hook primary + OTEL enrich" model:
// the hook supplies the project, OTEL skill_activated supplies the trigger, and
// the shared tool_use_id merges them into a single counted row — regardless of
// which feed arrives first.
func TestUsageEnrichmentBothFeeds(t *testing.T) {
	cases := []struct {
		name      string
		hookFirst bool
	}{
		{"hook-then-otel", true},
		{"otel-then-hook", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := state.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			hook := state.Invocation{SkillName: "brainstorming", ProjectSlug: "proj-a", Harness: "claude", Source: "hook", ToolUseID: "toolu_X"}
			otel := state.Invocation{SkillName: "brainstorming", Harness: "claude", Trigger: "proactive", Source: "otel", ToolUseID: "toolu_X"}

			if tc.hookFirst {
				if err := db.RecordInvocation(hook); err != nil {
					t.Fatal(err)
				}
				if _, err := db.RecordInvocations([]state.Invocation{otel}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := db.RecordInvocations([]state.Invocation{otel}); err != nil {
					t.Fatal(err)
				}
				if err := db.RecordInvocation(hook); err != nil {
					t.Fatal(err)
				}
			}

			cells, err := db.UsageMatrix()
			if err != nil {
				t.Fatal(err)
			}
			if len(cells) != 1 {
				t.Fatalf("cells = %+v, want 1 merged row", cells)
			}
			if cells[0].ProjectSlug != "proj-a" || cells[0].Count != 1 {
				t.Fatalf("cell = %+v, want proj-a count 1", cells[0])
			}
			var project, trigger, source string
			if err := db.QueryRow(`SELECT project_slug, trigger, source FROM invocations`).Scan(&project, &trigger, &source); err != nil {
				t.Fatal(err)
			}
			if project != "proj-a" || trigger != "proactive" {
				t.Fatalf("merged row project=%q trigger=%q, want proj-a/proactive", project, trigger)
			}
			if !strings.Contains(source, "hook") || !strings.Contains(source, "otel") {
				t.Fatalf("merged source = %q, want both feeds", source)
			}
		})
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
