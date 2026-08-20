package agentstatus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const maxHookPayload = 1 << 20

// hookEnvelope intentionally names only metadata needed for status reduction.
// Sensitive JSON fields may be decoded by encoding/json into nowhere, but are
// never copied into Event, TraceEntry, or persisted records.
type hookEnvelope struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	TurnID         string          `json:"turn_id"`
	PromptID       string          `json:"prompt_id"`
	AgentID        string          `json:"agent_id"`
	AgentType      string          `json:"agent_type"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	Notification   string          `json:"notification_type"`
	Source         string          `json:"source"`
	Reason         string          `json:"reason"`
	StopHookActive bool            `json:"stop_hook_active"`
}

func DecodeHook(provider Provider, pane PaneIdentity, r io.Reader, now time.Time) (Event, TraceEntry, error) {
	event := Event{Pane: pane, Provider: provider, ObservedAt: now}
	trace := TraceEntry{Pane: pane, Provider: provider, ObservedAt: now}
	if _, err := ParseProvider(string(provider)); err != nil {
		trace.ErrorClass = "provider"
		return event, trace, err
	}
	if err := validatePaneIdentity(pane); err != nil {
		trace.ErrorClass = "pane-identity"
		return event, trace, err
	}
	data, err := io.ReadAll(io.LimitReader(r, maxHookPayload+1))
	if err != nil {
		trace.ErrorClass = "read"
		return event, trace, fmt.Errorf("read hook input: %w", err)
	}
	if len(data) > maxHookPayload {
		trace.ErrorClass = "oversize"
		return event, trace, errors.New("hook payload exceeds 1 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var raw hookEnvelope
	if err := dec.Decode(&raw); err != nil {
		trace.ErrorClass = "json"
		return event, trace, fmt.Errorf("decode hook input: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		trace.ErrorClass = "trailing-json"
		return event, trace, errors.New("hook input must contain one JSON value")
	}
	raw.HookEventName, err = safeIdentifier(raw.HookEventName, 80, true)
	if err != nil || raw.HookEventName == "" {
		trace.ErrorClass = "event-name"
		return event, trace, errors.New("hook event name is missing or invalid")
	}
	raw.SessionID, err = safeIdentifier(raw.SessionID, 256, false)
	if err != nil || raw.SessionID == "" {
		trace.RawEvent = raw.HookEventName
		trace.ErrorClass = "session-id"
		return event, trace, errors.New("provider session id is missing or invalid")
	}
	turnID := raw.TurnID
	if turnID == "" {
		turnID = raw.PromptID
	}
	turnID, err = safeIdentifier(turnID, 256, false)
	if err != nil {
		trace.RawEvent = raw.HookEventName
		trace.ErrorClass = "turn-id"
		return event, trace, err
	}
	raw.AgentID, err = safeIdentifier(raw.AgentID, 256, false)
	if err != nil {
		trace.ErrorClass = "child-id"
		return event, trace, err
	}
	raw.AgentType, err = safeIdentifier(raw.AgentType, 128, true)
	if err != nil {
		trace.ErrorClass = "child-type"
		return event, trace, err
	}
	raw.Source, err = safeIdentifier(raw.Source, 40, false)
	if err != nil {
		trace.ErrorClass = "source"
		return event, trace, err
	}

	event.ProviderSessionID = raw.SessionID
	event.TurnID = turnID
	event.ChildID = raw.AgentID
	event.ChildType = raw.AgentType
	event.Source = raw.Source
	event.RawEvent = raw.HookEventName
	event.Kind, event.Reason = classifyHook(provider, raw)
	event.CorrelationID = hookCorrelation(raw)
	trace.ProviderSessionID = event.ProviderSessionID
	trace.TurnID = event.TurnID
	trace.CorrelationID = event.CorrelationID
	trace.RawEvent = event.RawEvent
	trace.Kind = event.Kind
	trace.Reason = event.Reason
	trace.Accepted = event.Kind != EventObservedOnly
	if !trace.Accepted {
		trace.ErrorClass = "unsupported-event"
	}
	return event, trace, nil
}

func classifyHook(provider Provider, raw hookEnvelope) (EventKind, string) {
	switch raw.HookEventName {
	case "SessionStart":
		return EventSessionStart, "session-start"
	case "UserPromptSubmit":
		return EventTurnStart, "user-prompt"
	case "PreToolUse":
		if structuredInputTool(raw.ToolName) {
			return EventAttentionConfirmed, "structured-input"
		}
		return EventProgress, "tool-start"
	case "PostToolUse":
		return EventAttentionResolved, "tool-finished"
	case "PostToolUseFailure":
		return EventFailure, "tool-failed"
	case "PostToolBatch":
		return EventProgress, "tool-batch-finished"
	case "PermissionRequest":
		if provider == ProviderClaude || structuredInputTool(raw.ToolName) {
			return EventAttentionConfirmed, "permission"
		}
		return EventAttentionCandidate, "permission-candidate"
	case "PermissionDenied":
		return EventFailure, "permission-denied"
	case "Notification":
		if provider == ProviderClaude && raw.Notification == "permission_prompt" {
			return EventAttentionConfirmed, "permission-notification"
		}
		return EventObservedOnly, "notification"
	case "Stop":
		return EventTurnStop, "response-completed"
	case "StopFailure":
		return EventFailure, "turn-failed"
	case "SessionEnd":
		return EventSessionEnd, "session-end"
	case "SubagentStart":
		return EventSubagentStart, "subagent-start"
	case "SubagentStop":
		return EventSubagentStop, "subagent-stop"
	default:
		return EventObservedOnly, "unsupported-event"
	}
}

func structuredInputTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "request_user_input" || name == "askuserquestion"
}

func hookCorrelation(raw hookEnvelope) string {
	var candidates []string
	if value, err := safeIdentifier(raw.ToolUseID, 256, false); err == nil && value != "" {
		candidates = append(candidates, "id:"+value)
	}
	if strings.TrimSpace(raw.ToolName) == "" || len(raw.ToolInput) == 0 || string(raw.ToolInput) == "null" {
		return strings.Join(candidates, "|")
	}
	var normalized any
	if json.Unmarshal(raw.ToolInput, &normalized) != nil {
		return strings.Join(candidates, "|")
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return strings.Join(candidates, "|")
	}
	sum := sha256.Sum256(append([]byte(raw.ToolName+"\x00"), canonical...))
	candidates = append(candidates, "sha256:"+hex.EncodeToString(sum[:16]))
	return strings.Join(candidates, "|")
}

func safeIdentifier(value string, max int, allowSpaces bool) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return "", fmt.Errorf("metadata identifier exceeds %d bytes", max)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || (!allowSpaces && r == ' ') {
			return "", errors.New("metadata identifier contains controls or spaces")
		}
	}
	return value, nil
}

func validatePaneIdentity(pane PaneIdentity) error {
	if pane.ServerID == "" || pane.PaneID == "" || pane.PanePID <= 0 || pane.TmuxSessionID == "" {
		return errors.New("incomplete tmux pane identity")
	}
	return nil
}
