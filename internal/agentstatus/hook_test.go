package agentstatus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeHookClassifiesCurrentProviderEvents(t *testing.T) {
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		provider  Provider
		payload   string
		kind      EventKind
		reason    string
		accepted  bool
		turnID    string
		childID   string
		childType string
	}{
		{"session start", ProviderCodex, `{"hook_event_name":"SessionStart","session_id":"s1","source":"resume"}`, EventSessionStart, "session-start", true, "", "", ""},
		{"codex prompt turn", ProviderCodex, `{"hook_event_name":"UserPromptSubmit","session_id":"s1","turn_id":"turn-1","prompt":"private"}`, EventTurnStart, "user-prompt", true, "turn-1", "", ""},
		{"claude prompt id", ProviderClaude, `{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt_id":"prompt-1","prompt":"private"}`, EventTurnStart, "user-prompt", true, "prompt-1", "", ""},
		{"ordinary tool", ProviderCodex, `{"hook_event_name":"PreToolUse","session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_input":{"command":"private"}}`, EventProgress, "tool-start", true, "t1", "", ""},
		{"codex structured input", ProviderCodex, `{"hook_event_name":"PreToolUse","session_id":"s1","turn_id":"t1","tool_name":"request_user_input","tool_use_id":"call-1"}`, EventAttentionConfirmed, "structured-input", true, "t1", "", ""},
		{"claude structured input", ProviderClaude, `{"hook_event_name":"PreToolUse","session_id":"s1","prompt_id":"p1","tool_name":"AskUserQuestion","tool_use_id":"call-2"}`, EventAttentionConfirmed, "structured-input", true, "p1", "", ""},
		{"codex permission candidate", ProviderCodex, `{"hook_event_name":"PermissionRequest","session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_use_id":"call-3"}`, EventAttentionCandidate, "permission-candidate", true, "t1", "", ""},
		{"claude permission confirmed", ProviderClaude, `{"hook_event_name":"PermissionRequest","session_id":"s1","prompt_id":"p1","tool_name":"Bash","tool_use_id":"call-4"}`, EventAttentionConfirmed, "permission", true, "p1", "", ""},
		{"permission notification", ProviderClaude, `{"hook_event_name":"Notification","session_id":"s1","prompt_id":"p1","notification_type":"permission_prompt","message":"private"}`, EventAttentionConfirmed, "permission-notification", true, "p1", "", ""},
		{"unrelated notification", ProviderClaude, `{"hook_event_name":"Notification","session_id":"s1","prompt_id":"p1","notification_type":"idle_prompt","message":"private"}`, EventObservedOnly, "notification", false, "p1", "", ""},
		{"post tool", ProviderClaude, `{"hook_event_name":"PostToolUse","session_id":"s1","prompt_id":"p1","tool_name":"Bash","tool_use_id":"call-4","tool_response":"private"}`, EventAttentionResolved, "tool-finished", true, "p1", "", ""},
		{"tool failure", ProviderClaude, `{"hook_event_name":"PostToolUseFailure","session_id":"s1","prompt_id":"p1","tool_name":"Bash","tool_use_id":"call-4","error":"private"}`, EventFailure, "tool-failed", true, "p1", "", ""},
		{"permission denied", ProviderClaude, `{"hook_event_name":"PermissionDenied","session_id":"s1","prompt_id":"p1","tool_name":"Bash","tool_use_id":"call-4"}`, EventFailure, "permission-denied", true, "p1", "", ""},
		{"stop", ProviderCodex, `{"hook_event_name":"Stop","session_id":"s1","turn_id":"t1","last_assistant_message":"private"}`, EventTurnStop, "response-completed", true, "t1", "", ""},
		{"stop failure", ProviderClaude, `{"hook_event_name":"StopFailure","session_id":"s1","prompt_id":"p1","error":"private"}`, EventFailure, "turn-failed", true, "p1", "", ""},
		{"session end", ProviderCodex, `{"hook_event_name":"SessionEnd","session_id":"s1"}`, EventSessionEnd, "session-end", true, "", "", ""},
		{"child start", ProviderCodex, `{"hook_event_name":"SubagentStart","session_id":"s1","turn_id":"t1","agent_id":"child-1","agent_type":"review"}`, EventSubagentStart, "subagent-start", true, "t1", "child-1", "review"},
		{"child stop", ProviderClaude, `{"hook_event_name":"SubagentStop","session_id":"s1","prompt_id":"p1","agent_id":"child-2","agent_type":"Explore","agent_transcript_path":"private"}`, EventSubagentStop, "subagent-stop", true, "p1", "child-2", "Explore"},
		{"future event trace only", ProviderClaude, `{"hook_event_name":"FutureEvent","session_id":"s1","prompt_id":"p1","secret":"private"}`, EventObservedOnly, "unsupported-event", false, "p1", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, trace, err := DecodeHook(tt.provider, testPane(), strings.NewReader(tt.payload), now)
			if err != nil {
				t.Fatal(err)
			}
			if event.Kind != tt.kind || event.Reason != tt.reason || event.TurnID != tt.turnID || event.ChildID != tt.childID || event.ChildType != tt.childType {
				t.Fatalf("event = %+v, want kind=%q reason=%q turn=%q child=%q/%q", event, tt.kind, tt.reason, tt.turnID, tt.childID, tt.childType)
			}
			if event.ProviderSessionID != "s1" || event.Provider != tt.provider || event.ObservedAt != now || event.Pane != testPane() {
				t.Fatalf("event identity = %+v", event)
			}
			if tt.name == "session start" && event.Source != "resume" {
				t.Fatalf("session source = %q, want resume", event.Source)
			}
			if trace.Accepted != tt.accepted || trace.Kind != tt.kind || trace.RawEvent != event.RawEvent || trace.ProviderSessionID != "s1" {
				t.Fatalf("trace = %+v", trace)
			}
			if tt.accepted && trace.ErrorClass != "" {
				t.Fatalf("accepted trace error class = %q", trace.ErrorClass)
			}
			if !tt.accepted && trace.ErrorClass != "unsupported-event" {
				t.Fatalf("observed-only error class = %q", trace.ErrorClass)
			}
		})
	}
}

func TestDecodeHookNeverReturnsSensitivePayloadFields(t *testing.T) {
	payload := `{
		"hook_event_name":"PreToolUse",
		"session_id":"session-safe",
		"turn_id":"turn-safe",
		"cwd":"SUPER_PRIVATE_CWD",
		"transcript_path":"SUPER_PRIVATE_TRANSCRIPT",
		"prompt":"SUPER_PRIVATE_PROMPT",
		"last_assistant_message":"SUPER_PRIVATE_RESPONSE",
		"message":"SUPER_PRIVATE_NOTIFICATION",
		"tool_name":"Bash",
		"tool_input":{"command":"SUPER_PRIVATE_TOOL_INPUT"}
	}`
	event, trace, err := DecodeHook(ProviderCodex, testPane(), strings.NewReader(payload), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		Event Event
		Trace TraceEntry
	}{event, trace})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SUPER_PRIVATE_CWD", "SUPER_PRIVATE_TRANSCRIPT", "SUPER_PRIVATE_PROMPT", "SUPER_PRIVATE_RESPONSE", "SUPER_PRIVATE_NOTIFICATION", "SUPER_PRIVATE_TOOL_INPUT"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("decoded metadata retained %q: %s", secret, encoded)
		}
	}
	if !strings.HasPrefix(event.CorrelationID, "sha256:") {
		t.Fatalf("correlation = %q, want privacy-safe digest", event.CorrelationID)
	}
}

func TestDecodeHookCorrelationIsStableAndPrefersToolUseID(t *testing.T) {
	now := time.Now()
	decode := func(payload string) Event {
		event, _, err := DecodeHook(ProviderClaude, testPane(), strings.NewReader(payload), now)
		if err != nil {
			t.Fatal(err)
		}
		return event
	}
	first := decode(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"b":2,"a":1}}`)
	second := decode(`{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"a":1,"b":2}}`)
	if first.CorrelationID == "" || first.CorrelationID != second.CorrelationID {
		t.Fatalf("canonical digests differ: %q != %q", first.CorrelationID, second.CorrelationID)
	}
	withID := decode(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_use_id":"tool-7","tool_input":{"secret":"ignored"}}`)
	if !strings.Contains(withID.CorrelationID, "id:tool-7") || !strings.Contains(withID.CorrelationID, "sha256:") {
		t.Fatalf("tool-use correlation = %q, want ID plus canonical digest", withID.CorrelationID)
	}
}

func TestDecodeClaudePostToolBatchRenewsProgressWithoutSensitiveData(t *testing.T) {
	event, trace, err := DecodeHook(
		ProviderClaude,
		testPane(),
		strings.NewReader(`{"hook_event_name":"PostToolBatch","session_id":"s1","prompt_id":"p1","tool_results":[{"output":"SUPER_PRIVATE_RESULT"}]}`),
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventProgress || event.Reason != "tool-batch-finished" || event.TurnID != "p1" {
		t.Fatalf("batch event = %+v", event)
	}
	encoded, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SUPER_PRIVATE_RESULT") {
		t.Fatalf("batch trace retained tool output: %s", encoded)
	}
}

func TestDecodeHookRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	tests := []struct {
		name       string
		provider   Provider
		pane       PaneIdentity
		payload    string
		errorClass string
	}{
		{"provider", Provider("other"), testPane(), `{"hook_event_name":"Stop","session_id":"s1"}`, "provider"},
		{"pane", ProviderCodex, PaneIdentity{PaneID: "%1"}, `{"hook_event_name":"Stop","session_id":"s1"}`, "pane-identity"},
		{"json", ProviderCodex, testPane(), `{`, "json"},
		{"trailing value", ProviderCodex, testPane(), `{"hook_event_name":"Stop","session_id":"s1"} {}`, "trailing-json"},
		{"missing event", ProviderCodex, testPane(), `{"session_id":"s1"}`, "event-name"},
		{"missing session", ProviderCodex, testPane(), `{"hook_event_name":"Stop"}`, "session-id"},
		{"session control", ProviderCodex, testPane(), "{\"hook_event_name\":\"Stop\",\"session_id\":\"bad\\nvalue\"}", "session-id"},
		{"turn space", ProviderCodex, testPane(), `{"hook_event_name":"Stop","session_id":"s1","turn_id":"bad value"}`, "turn-id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, trace, err := DecodeHook(tt.provider, tt.pane, strings.NewReader(tt.payload), time.Now())
			if err == nil {
				t.Fatal("error = nil")
			}
			if trace.ErrorClass != tt.errorClass || trace.Accepted {
				t.Fatalf("trace = %+v, want error class %q", trace, tt.errorClass)
			}
		})
	}
}

func TestDecodeHookRejectsPayloadOverOneMiB(t *testing.T) {
	payload := `{"hook_event_name":"Stop","session_id":"s1","padding":"` + strings.Repeat("x", maxHookPayload) + `"}`
	_, trace, err := DecodeHook(ProviderCodex, testPane(), strings.NewReader(payload), time.Now())
	if err == nil || trace.ErrorClass != "oversize" {
		t.Fatalf("err = %v, trace = %+v", err, trace)
	}
}

func TestDecodeHookBoundsMetadataIdentifiers(t *testing.T) {
	payload := `{"hook_event_name":"SubagentStart","session_id":"s1","agent_id":"` + strings.Repeat("x", 257) + `"}`
	_, trace, err := DecodeHook(ProviderClaude, testPane(), strings.NewReader(payload), time.Now())
	if err == nil || trace.ErrorClass != "child-id" {
		t.Fatalf("err = %v, trace = %+v", err, trace)
	}
}

func testPane() PaneIdentity {
	return PaneIdentity{ServerID: "server-1", PaneID: "%7", PanePID: 987, TmuxSessionID: "$2"}
}
