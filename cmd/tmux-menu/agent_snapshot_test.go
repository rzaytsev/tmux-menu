package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/tmux"
)

func TestAgentStatusSnapshotForRows(t *testing.T) {
	got := agentStatusSnapshotForRows([]agentRow{
		{
			pane:         tmux.Pane{PaneID: "%7"},
			provider:     agentstatus.ProviderCodex,
			status:       agentStatusCompleted,
			statusSource: "hook",
			fresh:        true,
		},
		{
			pane:         tmux.Pane{PaneID: "%9"},
			name:         "opencode",
			status:       agentStatusWaiting,
			statusSource: "process",
			fresh:        true,
		},
	})

	if got.Version != agentStatusSnapshotVersion || len(got.Agents) != 2 {
		t.Fatalf("snapshot = %#v", got)
	}
	if got.Agents[0].PaneID != "%7" || got.Agents[0].Provider != "codex" || got.Agents[0].Status != "completed" || got.Agents[0].Source != "hook" || !got.Agents[0].Fresh {
		t.Fatalf("codex row = %#v", got.Agents[0])
	}
	if got.Agents[1].PaneID != "%9" || got.Agents[1].Provider != "opencode" || got.Agents[1].Status != "waiting" {
		t.Fatalf("fallback provider row = %#v", got.Agents[1])
	}
}

func TestAgentStatusSnapshotV1RemainsMetadataOnly(t *testing.T) {
	snapshot := agentStatusSnapshotForRows([]agentRow{{
		pane:           tmux.Pane{PaneID: "%7"},
		provider:       agentstatus.ProviderCodex,
		status:         agentStatusWorking,
		statusSource:   "hook",
		fresh:          true,
		turnStartedAt:  time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		stateChangedAt: time.Date(2026, 8, 20, 12, 0, 1, 0, time.UTC),
		lastEventAt:    time.Date(2026, 8, 20, 12, 0, 2, 0, time.UTC),
	}})
	var out bytes.Buffer
	if err := json.NewEncoder(&out).Encode(snapshot); err != nil {
		t.Fatal(err)
	}
	encoded := out.String()
	for _, forbidden := range []string{"turn_started_at", "state_changed_at", "last_event_at", "terminal", "content", "focus", "page"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("snapshot v1 leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAgentHookSnapshotCommand(t *testing.T) {
	original := writeAgentStatusSnapshotCommand
	t.Cleanup(func() { writeAgentStatusSnapshotCommand = original })

	called := false
	writeAgentStatusSnapshotCommand = func(context.Context, io.Writer) error {
		called = true
		return nil
	}

	if err := runAgentHook(context.Background(), []string{"snapshot"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("snapshot command was not called")
	}
}
