// Package agentstatus reduces privacy-safe Codex and Claude hook metadata into
// annotations for live tmux panes.
package agentstatus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

func ParseProvider(value string) (Provider, error) {
	switch Provider(strings.ToLower(strings.TrimSpace(value))) {
	case ProviderCodex:
		return ProviderCodex, nil
	case ProviderClaude:
		return ProviderClaude, nil
	default:
		return "", fmt.Errorf("unsupported agent provider %q", value)
	}
}

type State string

const (
	StateAttention State = "attention"
	StateWorking   State = "working"
	StateCompleted State = "completed"
	StateWaiting   State = "waiting"
	StateUnknown   State = "unknown"
)

type PaneIdentity struct {
	ServerID      string `json:"server_id"`
	PaneID        string `json:"pane_id"`
	PanePID       int    `json:"pane_pid"`
	ProviderPID   int    `json:"provider_pid,omitempty"`
	TmuxSessionID string `json:"tmux_session_id,omitempty"`
}

type EventKind string

const (
	EventSessionStart       EventKind = "session-start"
	EventTurnStart          EventKind = "turn-start"
	EventProgress           EventKind = "progress"
	EventAttentionCandidate EventKind = "attention-candidate"
	EventAttentionConfirmed EventKind = "attention-confirmed"
	EventAttentionResolved  EventKind = "attention-resolved"
	EventTurnStop           EventKind = "turn-stop"
	EventSessionEnd         EventKind = "session-end"
	EventSubagentStart      EventKind = "subagent-start"
	EventSubagentStop       EventKind = "subagent-stop"
	EventObservedOnly       EventKind = "observed-only"
	EventFailure            EventKind = "failure"
)

type Event struct {
	Pane              PaneIdentity `json:"pane"`
	Provider          Provider     `json:"provider"`
	ProviderSessionID string       `json:"provider_session_id,omitempty"`
	TurnID            string       `json:"turn_id,omitempty"`
	ChildID           string       `json:"child_id,omitempty"`
	ChildType         string       `json:"child_type,omitempty"`
	Source            string       `json:"source,omitempty"`
	Kind              EventKind    `json:"kind"`
	RawEvent          string       `json:"raw_event"`
	Reason            string       `json:"reason,omitempty"`
	CorrelationID     string       `json:"correlation_id,omitempty"`
	ObservedAt        time.Time    `json:"observed_at"`
}

// TraceEntry deliberately contains only classified metadata. Hook payloads,
// prompts, tool arguments/results, cwd, transcripts, and model output never
// enter this type.
type TraceEntry struct {
	Provider          Provider     `json:"provider"`
	ProviderSessionID string       `json:"provider_session_id,omitempty"`
	TurnID            string       `json:"turn_id,omitempty"`
	CorrelationID     string       `json:"correlation_id,omitempty"`
	Pane              PaneIdentity `json:"pane"`
	RawEvent          string       `json:"raw_event,omitempty"`
	Kind              EventKind    `json:"kind,omitempty"`
	Reason            string       `json:"reason,omitempty"`
	ObservedAt        time.Time    `json:"observed_at"`
	Accepted          bool         `json:"accepted"`
	ErrorClass        string       `json:"error_class,omitempty"`
}

type Policy struct {
	WorkingTTL     time.Duration
	ChildRetention time.Duration
	LockTimeout    time.Duration
}

func DefaultPolicy() Policy {
	return Policy{WorkingTTL: 2 * time.Minute, ChildRetention: 30 * time.Minute, LockTimeout: 750 * time.Millisecond}
}

type Decision struct {
	Applied bool
	Reason  string
}

type ChildAnnotation struct {
	ID             string    `json:"id"`
	Type           string    `json:"type,omitempty"`
	State          State     `json:"state"`
	Reason         string    `json:"reason,omitempty"`
	RawEvent       string    `json:"raw_event,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
	TurnStartedAt  time.Time `json:"turn_started_at,omitempty"`
	StateChangedAt time.Time `json:"state_changed_at,omitempty"`
	LastEventAt    time.Time `json:"last_event_at,omitempty"`
	Fresh          bool      `json:"fresh"`
}

type Annotation struct {
	Provider          Provider          `json:"provider"`
	ProviderSessionID string            `json:"provider_session_id,omitempty"`
	TurnID            string            `json:"turn_id,omitempty"`
	Pane              PaneIdentity      `json:"pane"`
	State             State             `json:"state"`
	Reason            string            `json:"reason,omitempty"`
	Source            string            `json:"source"`
	RawEvent          string            `json:"raw_event,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
	TurnStartedAt     time.Time         `json:"turn_started_at,omitempty"`
	StateChangedAt    time.Time         `json:"state_changed_at,omitempty"`
	LastEventAt       time.Time         `json:"last_event_at,omitempty"`
	Fresh             bool              `json:"fresh"`
	Children          []ChildAnnotation `json:"children,omitempty"`
	AcknowledgeToken  string            `json:"acknowledge_token,omitempty"`
}

type LivePane struct {
	Pane     PaneIdentity
	Provider Provider
}

func ServerFingerprint(tmuxEnv string) string {
	parts := strings.Split(strings.TrimSpace(tmuxEnv), ",")
	server := ""
	if len(parts) >= 2 {
		server = strings.TrimSpace(parts[0]) + "," + strings.TrimSpace(parts[1])
	} else if len(parts) == 1 {
		server = strings.TrimSpace(parts[0])
	}
	if server == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(server))
	return hex.EncodeToString(sum[:16])
}

func DefaultStateDir() (string, error) {
	if root := strings.TrimSpace(os.Getenv("TMUX_MENU_AGENT_STATE_DIR")); root != "" {
		return filepath.Abs(root)
	}
	if stateHome := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); stateHome != "" {
		return filepath.Join(stateHome, "tmux-menu", "agent-status", "v1"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve agent state home: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("resolve agent state home: empty home")
	}
	return filepath.Join(home, ".local", "state", "tmux-menu", "agent-status", "v1"), nil
}
