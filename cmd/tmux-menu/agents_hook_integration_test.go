package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"tmux-menu/internal/action"
	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

func TestAgentRowsUseFreshMatchingHookAndNeverCreateOrphans(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", PaneID: "%1", PanePID: "101", CurrentCommand: "zsh", PaneTitle: "shell"},
		{SessionName: "work", SessionID: "$1", PaneID: "%2", PanePID: "202", CurrentCommand: "codex", PaneTitle: "Ready"},
	}
	annotations := map[string]agentstatus.Annotation{
		"%1":  hookAnnotation(panes[0], agentstatus.ProviderClaude, agentstatus.StateWorking, now),
		"%2":  hookAnnotation(panes[1], agentstatus.ProviderClaude, agentstatus.StateAttention, now),
		"%99": {Pane: agentstatus.PaneIdentity{PaneID: "%99", PanePID: 999}, Provider: agentstatus.ProviderCodex, State: agentstatus.StateWorking, Fresh: true},
	}
	rows := agentRowsForPanes(panes, processSnapshot{}, annotations)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want two live rows with no orphan: %#v", len(rows), rows)
	}
	if rows[0].pane.PaneID != "%1" || rows[0].provider != agentstatus.ProviderClaude || rows[0].status != agentStatusWorking {
		t.Fatalf("shell pane was not claimed by its fresh hook: %#v", rows[0])
	}
	if rows[1].pane.PaneID != "%2" || rows[1].status != agentStatusWaiting {
		t.Fatalf("provider-mismatched hook should fall back to Codex title: %#v", rows[1])
	}
}

func TestSharedAgentInventoryPreservesExactIdentityAndSemanticParity(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "101", CurrentCommand: "codex", PaneTitle: "Ready", CurrentPath: "/tmp/project"},
		{SessionName: "work", SessionID: "$1", WindowID: "@2", PaneID: "%2", PanePID: "202", CurrentCommand: "claude", PaneTitle: "✳ Claude Code", CurrentPath: "/tmp/project"},
	}
	attention := hookAnnotation(panes[1], agentstatus.ProviderClaude, agentstatus.StateAttention, now)
	attention.RawEvent = "PermissionRequest"
	attention.Source = "hook"

	inventory := projectLiveAgentInventory(panes, processSnapshot{}, map[string]agentstatus.Annotation{"%2": attention})
	if got := []string{inventory[0].pane.PaneID, inventory[1].pane.PaneID}; !reflect.DeepEqual(got, []string{"%1", "%2"}) {
		t.Fatalf("stable inventory order = %#v, want live pane order", got)
	}
	pickerRows := agentRowsForPicker(inventory)
	if got := []string{pickerRows[0].pane.PaneID, pickerRows[1].pane.PaneID}; !reflect.DeepEqual(got, []string{"%2", "%1"}) {
		t.Fatalf("picker severity order = %#v", got)
	}
	items := agentItemsForRows(pickerRows, "", nil, config.Default().Agents, true)
	snapshot := agentStatusSnapshotForRows(agentRowsForSnapshot(inventory))
	snapshotByPane := make(map[string]agentStatusSnapshotRow, len(snapshot.Agents))
	for _, row := range snapshot.Agents {
		snapshotByPane[row.PaneID] = row
	}
	for index, row := range pickerRows {
		item := items[index]
		dispatch := item.Value.dispatch
		if item.Preview != row.pane.PaneID || dispatch.SessionID != row.pane.SessionID || dispatch.WindowID != row.pane.WindowID || dispatch.PaneID != row.pane.PaneID {
			t.Fatalf("picker identity for %s = preview %q dispatch %+v", row.pane.PaneID, item.Preview, dispatch)
		}
		got := snapshotByPane[row.pane.PaneID]
		if got.Provider != nonEmpty(string(row.provider), row.name) || got.Status != string(row.status) || got.Source != row.statusSource || got.Fresh != row.fresh {
			t.Fatalf("semantic parity for %s: inventory=%+v snapshot=%+v", row.pane.PaneID, row, got)
		}
	}
}

func TestSharedAgentInventoryRejectsMalformedIdentityAndControlMetadata(t *testing.T) {
	base := tmux.Pane{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "101", CurrentCommand: "codex", PaneTitle: "Ready", CurrentPath: "/tmp/project"}
	badSessionID := base
	badSessionID.PaneID, badSessionID.SessionID = "%2", "work"
	badWindowID := base
	badWindowID.PaneID, badWindowID.WindowID = "%3", "1"
	badPaneID := base
	badPaneID.PaneID = "3"
	badPID := base
	badPID.PaneID, badPID.PanePID = "%4", "not-a-pid"
	separatorMetadata := base
	separatorMetadata.PaneID, separatorMetadata.SessionName = "%5", "work\x1fshifted"
	newlineMetadata := base
	newlineMetadata.PaneID, newlineMetadata.PaneTitle = "%6", "Ready\nshifted"

	rows := projectLiveAgentInventory([]tmux.Pane{base, badSessionID, badWindowID, badPaneID, badPID, separatorMetadata, newlineMetadata}, processSnapshot{}, nil)
	if len(rows) != 1 || rows[0].pane.PaneID != "%1" {
		t.Fatalf("malformed live identities reached inventory: %+v", rows)
	}
}

func TestDuplicateAgentLabelsRetainDifferentStableTargets(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "101", CurrentCommand: "codex", PaneTitle: "Ready", CurrentPath: "/tmp/project"},
		{SessionName: "work", SessionID: "$1", WindowID: "@2", PaneID: "%2", PanePID: "202", CurrentCommand: "codex", PaneTitle: "Ready", CurrentPath: "/tmp/project"},
	}
	rows := projectLiveAgentInventory(panes, processSnapshot{}, nil)
	items := agentItemsForRows(agentRowsForPicker(rows), "", nil, config.Default().Agents, true)
	if items[0].Label != items[1].Label {
		t.Fatalf("fixture labels are not duplicates: %q / %q", items[0].Label, items[1].Label)
	}
	if items[0].Value.dispatch.PaneID == items[1].Value.dispatch.PaneID || items[0].Value.dispatch.WindowID == items[1].Value.dispatch.WindowID {
		t.Fatalf("duplicate labels collapsed stable identities: %+v / %+v", items[0].Value.dispatch, items[1].Value.dispatch)
	}
}

func TestAgentRowsIgnoreStaleHookAndRestrictTitleToProvider(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	pane := tmux.Pane{PaneID: "%2", PanePID: "202", CurrentCommand: "codex", PaneTitle: "✽ Claude Code"}
	annotation := hookAnnotation(pane, agentstatus.ProviderCodex, agentstatus.StateCompleted, now)
	annotation.Fresh = false
	rows := agentRowsForPanes([]tmux.Pane{pane}, processSnapshot{statuses: map[int]agentStatus{202: agentStatusWaiting}}, map[string]agentstatus.Annotation{"%2": annotation})
	if len(rows) != 1 || rows[0].status != agentStatusWaiting {
		t.Fatalf("stale hook or cross-provider Claude title overrode process fallback: %#v", rows)
	}
}

func TestAgentRowsExposeTimingOnlyForFreshAuthoritativeHookEvidence(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	pane := tmux.Pane{SessionID: "$1", WindowID: "@2", PaneID: "%2", PanePID: "202", CurrentCommand: "codex", PaneTitle: "Working"}
	annotation := hookAnnotation(pane, agentstatus.ProviderCodex, agentstatus.StateWorking, now)
	annotation.TurnStartedAt = now.Add(-time.Minute)
	annotation.StateChangedAt = now.Add(-30 * time.Second)
	annotation.LastEventAt = now
	rows := agentRowsForPanes([]tmux.Pane{pane}, processSnapshot{}, map[string]agentstatus.Annotation{"%2": annotation})
	if len(rows) != 1 || !rows[0].turnStartedAt.Equal(annotation.TurnStartedAt) || !rows[0].stateChangedAt.Equal(annotation.StateChangedAt) || !rows[0].lastEventAt.Equal(annotation.LastEventAt) {
		t.Fatalf("fresh hook timing = %+v", rows)
	}

	annotation.Fresh = false
	rows = agentRowsForPanes([]tmux.Pane{pane}, processSnapshot{}, map[string]agentstatus.Annotation{"%2": annotation})
	if len(rows) != 1 || !rows[0].turnStartedAt.IsZero() || !rows[0].stateChangedAt.IsZero() || !rows[0].lastEventAt.IsZero() {
		t.Fatalf("fallback row exposed unsupported timing: %+v", rows)
	}
}

func TestAgentRowsRejectPaneIncarnationMismatch(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	pane := tmux.Pane{SessionID: "$2", PaneID: "%2", PanePID: "202", CurrentCommand: "codex", PaneTitle: "Ready"}
	annotation := hookAnnotation(pane, agentstatus.ProviderCodex, agentstatus.StateWorking, now)
	annotation.Pane.PanePID = 999
	annotation.Pane.TmuxSessionID = "$1"
	rows := agentRowsForPanes([]tmux.Pane{pane}, processSnapshot{}, map[string]agentstatus.Annotation{"%2": annotation})
	if len(rows) != 1 || rows[0].status != agentStatusWaiting {
		t.Fatalf("mismatched pane incarnation should use live title fallback: %#v", rows)
	}
}

func TestAgentRowsRejectExitedProviderIncarnationAfterSuccessfulProcessScan(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	pane := tmux.Pane{SessionID: "$2", PaneID: "%2", PanePID: "202", CurrentCommand: "zsh", PaneTitle: "shell"}
	annotation := hookAnnotation(pane, agentstatus.ProviderCodex, agentstatus.StateAttention, now)
	annotation.Pane.ProviderPID = 303

	rows := agentRowsForPanes([]tmux.Pane{pane}, processSnapshot{available: true}, map[string]agentstatus.Annotation{"%2": annotation})
	if len(rows) != 0 {
		t.Fatalf("exited provider claim created a shell row: %#v", rows)
	}

	rows = agentRowsForPanes([]tmux.Pane{pane}, processSnapshot{}, map[string]agentstatus.Annotation{"%2": annotation})
	if len(rows) != 1 || rows[0].provider != agentstatus.ProviderCodex {
		t.Fatalf("unavailable process scan should preserve matching pane claim: %#v", rows)
	}
}

func TestBuildProcessSnapshotTracksProviderIncarnationPerPaneRoot(t *testing.T) {
	snapshot := buildProcessSnapshot([]processInfo{
		{pid: 100, ppid: 1, state: "S", command: "zsh"},
		{pid: 200, ppid: 100, state: "S", command: "codex"},
		{pid: 300, ppid: 100, state: "S", command: "claude"},
	})
	if !snapshot.available || snapshot.providerPIDs[100][agentstatus.ProviderCodex] != 200 || snapshot.providerPIDs[100][agentstatus.ProviderClaude] != 300 {
		t.Fatalf("provider incarnations = %#v", snapshot.providerPIDs)
	}
}

func TestCodexPermissionCandidateNeedsTitleConfirmation(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	pane := tmux.Pane{PaneID: "%2", PanePID: "202", CurrentCommand: "codex", PaneTitle: "Ready"}
	annotation := hookAnnotation(pane, agentstatus.ProviderCodex, agentstatus.StateAttention, now)
	annotation.RawEvent = "PermissionRequest"
	rows := agentRowsForPanes([]tmux.Pane{pane}, processSnapshot{}, map[string]agentstatus.Annotation{"%2": annotation})
	if len(rows) != 1 || rows[0].status != agentStatusWaiting {
		t.Fatalf("unconfirmed Codex PermissionRequest should retain title evidence: %#v", rows)
	}
	pane.PaneTitle = "Action Required"
	rows = agentRowsForPanes([]tmux.Pane{pane}, processSnapshot{}, map[string]agentstatus.Annotation{"%2": annotation})
	if len(rows) != 1 || rows[0].status != agentStatusAttention {
		t.Fatalf("confirmed Codex PermissionRequest should surface attention: %#v", rows)
	}
}

func TestAgentRowsSortBySeverityThenHookRecencyAndRollUpChildren(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	panes := []tmux.Pane{
		{SessionName: "work", PaneID: "%1", PanePID: "101", CurrentCommand: "codex", PaneTitle: "Ready"},
		{SessionName: "work", PaneID: "%2", PanePID: "202", CurrentCommand: "claude", PaneTitle: "✳ Claude Code"},
		{SessionName: "work", PaneID: "%3", PanePID: "303", CurrentCommand: "codex", PaneTitle: "Ready"},
		{SessionName: "work", PaneID: "%4", PanePID: "404", CurrentCommand: "codex", PaneTitle: "Ready"},
	}
	attention := hookAnnotation(panes[0], agentstatus.ProviderCodex, agentstatus.StateAttention, now.Add(-time.Minute))
	attention.RawEvent = "PreToolUse"
	attention.Reason = "structured-input"
	workingOld := hookAnnotation(panes[1], agentstatus.ProviderClaude, agentstatus.StateWorking, now.Add(-2*time.Minute))
	workingNew := hookAnnotation(panes[2], agentstatus.ProviderCodex, agentstatus.StateWorking, now.Add(-time.Minute))
	completed := hookAnnotation(panes[3], agentstatus.ProviderCodex, agentstatus.StateCompleted, now)
	completed.AcknowledgeToken = "completion-token"
	completed.Children = []agentstatus.ChildAnnotation{{ID: "child-1", Type: "review", State: agentstatus.StateWorking, RawEvent: "SubagentStart", Fresh: true}}
	rows := agentRowsForPicker(agentRowsForPanes(panes, processSnapshot{}, map[string]agentstatus.Annotation{
		"%1": attention, "%2": workingOld, "%3": workingNew, "%4": completed,
	}))
	got := []string{rows[0].pane.PaneID, rows[1].pane.PaneID, rows[2].pane.PaneID, rows[3].pane.PaneID}
	if want := []string{"%1", "%3", "%2", "%4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered pane IDs = %#v, want %#v", got, want)
	}
	if rows[0].acknowledgeToken != "" || rows[3].acknowledgeToken != "completion-token" {
		t.Fatalf("ack token must be exposed only for completed evidence: %#v / %#v", rows[0], rows[3])
	}
	items := agentItemsForRows(rows, "", nil, config.Default().Agents, true)
	if label := stripANSI(items[3].Label); !strings.Contains(label, "✓") || !strings.Contains(label, "+1") {
		t.Fatalf("completed row should show completed icon and child rollup: %q", label)
	}
	if items[3].Value.agentPaneID != "%4" || items[3].Value.agentAckToken != "completion-token" {
		t.Fatalf("stable pane/ack metadata missing from rendered item: %#v", items[3].Value)
	}
}

func TestAgentInitialIndexUsesStablePaneIDAfterResort(t *testing.T) {
	items := []picker.Item[menuItem]{
		{Value: menuItem{agentPaneID: "%9"}},
		{Value: menuItem{agentPaneID: "%3"}},
		{Value: menuItem{agentPaneID: "%7"}},
	}
	if got := agentInitialIndex(items, "%7"); got != 2 {
		t.Fatalf("initial index = %d, want 2", got)
	}
	if got := agentInitialIndex(items, "%99"); got != 0 {
		t.Fatalf("missing stable pane should fall back to first row, got %d", got)
	}
}

func TestAgentPreviewRowsFreezeRenderedEvidence(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	rows := []agentRow{{
		pane: tmux.Pane{PaneID: "%1"}, provider: agentstatus.ProviderClaude, status: agentStatusCompleted,
		providerSession: "session-123", statusSource: "hook", rawEvent: "Stop", reason: "turn-stop", updatedAt: now.Add(-5 * time.Second), fresh: true,
		children: []agentstatus.ChildAnnotation{{ID: "child-1", Type: "review", State: agentstatus.StateCompleted, RawEvent: "SubagentStop"}},
	}}
	preview := agentPreviewRows(rows, now)
	rows[0].status = agentStatusWorking
	rows[0].children[0].ID = "mutated"
	got := preview["%1"]
	if got.State != "completed" || got.Provider != "claude" || got.ProviderSession != "session-123" || got.Source != "hook" || got.Event != "Stop" || got.Age != "5s ago" {
		t.Fatalf("frozen preview metadata = %#v", got)
	}
	if len(got.Children) != 1 || !strings.Contains(got.Children[0], "child-1") || strings.Contains(got.Children[0], "mutated") {
		t.Fatalf("frozen preview children = %#v", got.Children)
	}
}

func TestRunPickerLoopRefreshesAndReselectsStableAgentPane(t *testing.T) {
	dispatchPath := filepath.Join(t.TempDir(), "dispatch.json")
	t.Setenv("TMUX_MENU_DISPATCH_FILE", dispatchPath)
	oldSelect := selectModeForLoop
	oldAck := acknowledgeAgent
	defer func() { selectModeForLoop, acknowledgeAgent = oldSelect, oldAck }()
	var initial []string
	selectModeForLoop = func(_ context.Context, mode, initialPaneID string) (picker.Result[menuItem], error) {
		initial = append(initial, initialPaneID)
		if len(initial) == 1 {
			return picker.Result[menuItem]{Key: "ctrl-r", Selected: true, Value: menuItem{agentPaneID: "%7"}}, nil
		}
		return picker.Result[menuItem]{Selected: true, Value: menuItem{agentPaneID: "%7", dispatch: action.Dispatch{Mode: "switch-pane", PaneID: "%7"}}}, nil
	}
	if err := runPickerLoop(context.Background(), "agents"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"", "%7"}; !reflect.DeepEqual(initial, want) {
		t.Fatalf("initial stable pane sequence = %#v, want %#v", initial, want)
	}
	dispatched, err := action.Read(dispatchPath)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.PaneID != "%7" {
		t.Fatalf("Enter dispatch lost stable pane ID: %#v", dispatched)
	}
}

func TestRunPickerLoopAcknowledgesExactRenderedCompletion(t *testing.T) {
	oldSelect := selectModeForLoop
	oldAck := acknowledgeAgent
	defer func() { selectModeForLoop, acknowledgeAgent = oldSelect, oldAck }()
	calls := 0
	selectModeForLoop = func(_ context.Context, mode, initialPaneID string) (picker.Result[menuItem], error) {
		calls++
		if calls == 1 {
			return picker.Result[menuItem]{Key: "ctrl-x", Selected: true, Value: menuItem{agentPaneID: "%8", agentAckToken: "cas-token"}}, nil
		}
		if initialPaneID != "%8" {
			t.Fatalf("ack rebuild did not preserve pane: %q", initialPaneID)
		}
		return picker.Result[menuItem]{}, nil
	}
	var acknowledged string
	acknowledgeAgent = func(_ context.Context, token string) error {
		acknowledged = token
		return nil
	}
	if err := runPickerLoop(context.Background(), "agents"); err != nil {
		t.Fatal(err)
	}
	if acknowledged != "cas-token" {
		t.Fatalf("acknowledged token = %q, want exact rendered token", acknowledged)
	}
}

func TestRunPickerLoopRebuildsWhenRenderedCompletionIsStale(t *testing.T) {
	oldSelect := selectModeForLoop
	oldAck := acknowledgeAgent
	defer func() { selectModeForLoop, acknowledgeAgent = oldSelect, oldAck }()
	calls := 0
	selectModeForLoop = func(_ context.Context, mode, initialPaneID string) (picker.Result[menuItem], error) {
		calls++
		if calls == 1 {
			return picker.Result[menuItem]{Key: "ctrl-x", Selected: true, Value: menuItem{agentPaneID: "%8", agentAckToken: "stale-token"}}, nil
		}
		if initialPaneID != "%8" {
			t.Fatalf("stale acknowledgement rebuild did not preserve pane: %q", initialPaneID)
		}
		return picker.Result[menuItem]{}, nil
	}
	acknowledgeAgent = func(context.Context, string) error {
		return agentstatus.ErrNotAcknowledgeable
	}
	if err := runPickerLoop(context.Background(), "agents"); err != nil {
		t.Fatalf("stale rendered completion should rebuild, got %v", err)
	}
}

func TestRunPickerLoopNeverAcknowledgesAttentionWithoutToken(t *testing.T) {
	oldSelect := selectModeForLoop
	oldAck := acknowledgeAgent
	defer func() { selectModeForLoop, acknowledgeAgent = oldSelect, oldAck }()
	calls := 0
	selectModeForLoop = func(_ context.Context, mode, initialPaneID string) (picker.Result[menuItem], error) {
		calls++
		if calls == 1 {
			return picker.Result[menuItem]{Key: "ctrl-x", Selected: true, Value: menuItem{agentPaneID: "%9"}}, nil
		}
		return picker.Result[menuItem]{}, nil
	}
	acknowledgeAgent = func(_ context.Context, token string) error {
		t.Fatalf("attention without a completion token must not acknowledge: %q", token)
		return nil
	}
	if err := runPickerLoop(context.Background(), "agents"); err != nil {
		t.Fatal(err)
	}
}

func hookAnnotation(pane tmux.Pane, provider agentstatus.Provider, state agentstatus.State, updatedAt time.Time) agentstatus.Annotation {
	pid, _ := strconv.Atoi(pane.PanePID)
	return agentstatus.Annotation{
		Pane:      agentstatus.PaneIdentity{PaneID: pane.PaneID, PanePID: pid, TmuxSessionID: pane.SessionID},
		Provider:  provider,
		State:     state,
		Source:    "hook",
		RawEvent:  "event",
		UpdatedAt: updatedAt,
		Fresh:     true,
	}
}
