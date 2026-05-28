package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// OTLP/JSON log payload structures. We model only the subset of the
// ExportLogsServiceRequest schema we need to read Claude Code skill events.
// See https://opentelemetry.io/docs/specs/otlp/ and Claude Code monitoring docs.

type otlpLogsPayload struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

type otlpResourceLogs struct {
	Resource  otlpResource    `json:"resource"`
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeLogs struct {
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpLogRecord struct {
	TimeUnixNano string         `json:"timeUnixNano"`
	EventName    string         `json:"eventName"`
	Body         otlpAnyValue   `json:"body"`
	Attributes   []otlpKeyValue `json:"attributes"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

// otlpAnyValue covers the scalar AnyValue shapes Claude Code emits. We only
// read string-ish values; numeric values are stringified for our text fields.
type otlpAnyValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *string  `json:"intValue"`
	BoolValue   *bool    `json:"boolValue"`
	DoubleValue *float64 `json:"doubleValue"`
}

func (v otlpAnyValue) asString() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'f', -1, 64)
	}
	return ""
}

// parseOTLPSkillInvocations walks an OTLP/JSON logs export and returns the
// skill activations it can derive. Two event shapes are recognized:
//
//   - claude_code.tool_result with tool_name="Skill" — the real signal emitted
//     by current Claude Code (requires OTEL_LOG_TOOL_DETAILS=1 for skill_name).
//   - claude_code.skill_activated — a forward-compatible event name in case
//     Claude Code adds a dedicated skill activation event later.
//
// Project attribution is not available from OTEL standard attributes, so
// ProjectSlug is left empty; the PreToolUse hook fallback supplies it.
func parseOTLPSkillInvocations(data []byte) ([]state.Invocation, error) {
	var payload otlpLogsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse otlp logs: %w", err)
	}

	var invs []state.Invocation
	for _, rl := range payload.ResourceLogs {
		resourceAttrs := attrMap(rl.Resource.Attributes)
		for _, sl := range rl.ScopeLogs {
			for _, rec := range sl.LogRecords {
				attrs := attrMap(rec.Attributes)
				// Resource attributes (e.g. terminal.type) supplement record
				// attributes without overriding them.
				for k, v := range resourceAttrs {
					if _, ok := attrs[k]; !ok {
						attrs[k] = v
					}
				}
				if inv, ok := skillInvocationFromEvent(rec, attrs); ok {
					invs = append(invs, inv)
				}
			}
		}
	}
	return invs, nil
}

func skillInvocationFromEvent(rec otlpLogRecord, attrs map[string]string) (state.Invocation, bool) {
	event := eventName(rec, attrs)

	var skill string
	switch event {
	case "skill_activated", "claude_code.skill_activated":
		skill = firstNonEmpty(attrs["skill_name"], attrs["skill.name"])
	case "tool_result", "claude_code.tool_result", "tool_decision", "claude_code.tool_decision":
		if !strings.EqualFold(attrs["tool_name"], "Skill") {
			return state.Invocation{}, false
		}
		skill = firstNonEmpty(
			attrs["skill_name"],
			attrs["skill.name"],
			jsonField(attrs["tool_parameters"], "skill_name", "skill", "name"),
			jsonField(attrs["tool_input"], "skill_name", "skill", "name"),
		)
	default:
		return state.Invocation{}, false
	}
	if skill == "" {
		return state.Invocation{}, false
	}

	return state.Invocation{
		SkillName: skill,
		Harness:   firstNonEmpty(attrs["harness"], "claude"),
		Trigger:   deriveTrigger(attrs),
		InvokedAt: deriveTimestamp(rec, attrs),
		Source:    "otel",
	}, true
}

// eventName resolves the logical event name from the OTLP eventName field, the
// event.name attribute, or the log body, whichever is present.
func eventName(rec otlpLogRecord, attrs map[string]string) string {
	if rec.EventName != "" {
		return rec.EventName
	}
	if n := attrs["event.name"]; n != "" {
		return n
	}
	return rec.Body.asString()
}

// deriveTrigger maps available attributes to the invocation trigger taxonomy.
// Claude Code does not emit an explicit trigger today, so we honor an explicit
// attribute when present and otherwise infer "nested" for subagent-issued
// events, defaulting to "user-initiated".
func deriveTrigger(attrs map[string]string) string {
	if t := firstNonEmpty(attrs["invocation_trigger"], attrs["trigger"]); t != "" {
		return t
	}
	if firstNonEmpty(attrs["agent.name"], attrs["agent_id"], attrs["subagent_type"]) != "" {
		return "nested"
	}
	return "user-initiated"
}

// deriveTimestamp prefers the ISO-8601 event.timestamp attribute, falling back
// to the record's nanosecond clock, and finally to now (handled downstream).
func deriveTimestamp(rec otlpLogRecord, attrs map[string]string) string {
	if ts := attrs["event.timestamp"]; ts != "" {
		return ts
	}
	if rec.TimeUnixNano != "" {
		if nanos, err := strconv.ParseInt(rec.TimeUnixNano, 10, 64); err == nil && nanos > 0 {
			return time.Unix(0, nanos).UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func attrMap(kvs []otlpKeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		m[kv.Key] = kv.Value.asString()
	}
	return m
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// jsonField parses a JSON object string and returns the first present key.
// Used to dig skill names out of the tool_parameters/tool_input JSON blobs.
func jsonField(raw string, keys ...string) string {
	if raw == "" {
		return ""
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// runOTELReceiver starts a small HTTP server that accepts OTLP/HTTP JSON log
// exports from Claude Code at /v1/logs and records skill activations.
func runOTELReceiver(args []string, stdout, stderr io.Writer, gf globalFlags) int {
	opts, err := parseReceiverOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		handleOTLPLogs(w, r, home, stderr)
	})

	addr := net.JoinHostPort(opts.host, strconv.Itoa(opts.port))
	out := gf.outWriter(stdout)
	fmt.Fprintf(out, "skills-manager OTEL receiver listening on http://%s/v1/logs\n\n", addr)
	fmt.Fprint(out, otelSetupInstructions(addr))

	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(stderr, "otel receiver: %v\n", err)
		return ExitOpError
	}
	return ExitSuccess
}

func handleOTLPLogs(w http.ResponseWriter, r *http.Request, home string, stderr io.Writer) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20)) // 32 MiB cap
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	invs, err := parseOTLPSkillInvocations(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(invs) > 0 {
		db, err := state.Open(home)
		if err != nil {
			fmt.Fprintf(stderr, "otel receiver: open state: %v\n", err)
			http.Error(w, "state unavailable", http.StatusInternalServerError)
			return
		}
		defer db.Close()
		if _, err := db.RecordInvocations(invs); err != nil {
			fmt.Fprintf(stderr, "otel receiver: record: %v\n", err)
			http.Error(w, "record failed", http.StatusInternalServerError)
			return
		}
	}

	// OTLP/HTTP expects an ExportLogsServiceResponse; an empty JSON object is a
	// valid "all accepted" response.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

type receiverOptions struct {
	port int
	host string
}

func parseReceiverOptions(args []string) (receiverOptions, error) {
	opts := receiverOptions{port: 4318, host: "127.0.0.1"}
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

func otelSetupInstructions(addr string) string {
	return `Configure Claude Code to export skill activations to this receiver:

  export CLAUDE_CODE_ENABLE_TELEMETRY=1
  export OTEL_LOGS_EXPORTER=otlp
  export OTEL_EXPORTER_OTLP_PROTOCOL=http/json
  export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://` + addr + `/v1/logs
  export OTEL_LOG_TOOL_DETAILS=1   # required so skill names appear on tool events

Then run Claude Code as usual. Skill activations land in the invocations table;
view them with: skills-manager usage
`
}
