package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

func TestAgentStateAgeUsesTransitionsNotObservationOrProgressTimes(t *testing.T) {
	now := time.Now()
	row := agentRow{statusSource: "hook", stateChangedAt: now.Add(-4 * time.Minute), updatedAt: now, lastEventAt: now}
	if got := agentStateAge(row, now); got != "4m" {
		t.Fatalf("state age = %q, want transition age", got)
	}
	for _, source := range []string{"terminal-title", "process", "osc7501"} {
		row.statusSource = source
		if got := agentStateAge(row, now); got != "—" {
			t.Fatalf("%s supplied an unproven state age: %q", source, got)
		}
	}
	row.statusSource, row.stateChangedAt = "hook", now.Add(time.Second)
	if got := agentStateAge(row, now); got != "—" {
		t.Fatalf("future transition age = %q", got)
	}
}

func TestLiveRowsSearchFullFieldsAndCarryExactAckToken(t *testing.T) {
	if _, err := exec.LookPath("fzf"); err != nil {
		t.Skip("fzf is required for matching integration")
	}
	rows := []agentRow{
		{pane: tmux.Pane{PaneID: "%1", SessionID: "$1", SessionName: "first", PaneTitle: strings.Repeat("long ", 50) + "unique-tail", CurrentPath: "/tmp/project"}, name: "codex", status: agentStatusCompleted, acknowledgeToken: "displayed-completion-token"},
		{pane: tmux.Pane{PaneID: "%2", SessionID: "$2", SessionName: "second", PaneTitle: "other", CurrentPath: "/tmp/elsewhere"}, name: "codex", status: agentStatusAttention},
	}
	snapshot := agentPreviewSnapshot{Agents: config.Default().Agents, OriginSession: "$1"}
	var out bytes.Buffer
	if err := writeAgentPickerRows(t.Context(), snapshot, rows, nil, 80, "unique-tail", &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[2], "0\t%1\t") || strings.Contains(lines[2], "unique-tail") {
		t.Fatalf("truncated title should remain searchable: %q", out.String())
	}
	fields := strings.Split(lines[2], "\t")
	var metadata agentPickerMetadata
	if err := decodeAgentPickerMetadata(fields[3], &metadata); err != nil || metadata.AckToken != rows[0].acknowledgeToken || metadata.Workdir != "/tmp/project" {
		t.Fatalf("exact selection metadata = %+v, %v", metadata, err)
	}
	items, _ := agentPickerItemsForRows(rows, "", nil, snapshot.Agents, 80)
	parsed, err := picker.EncodeItems(items, true)
	if err != nil || !strings.Contains(parsed, fields[3]) {
		t.Fatal("initial and refreshed wire records differ")
	}
	for _, test := range []struct {
		filter string
		local  bool
		want   string
	}{
		{"attention", false, "1\t%2\t"}, {"completed", false, "0\t%1\t"},
		{"", true, "0\t%1\t"}, {"attention", true, "-1\t\tNo matching agents"},
	} {
		snapshot.Filter, snapshot.SessionOnly = test.filter, test.local
		out.Reset()
		if err := writeAgentPickerRows(t.Context(), snapshot, rows, nil, 120, "", &out); err != nil {
			t.Fatal(err)
		}
		lines = strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 3 || !strings.HasPrefix(lines[2], test.want) {
			t.Fatalf("filter %q local=%v: %q", test.filter, test.local, out.String())
		}
	}
}

func TestFrozenLiveRowsKeepOrderAndRejectReusedOrMovedPanes(t *testing.T) {
	first := agentRow{pane: tmux.Pane{PaneID: "%1", PanePID: "100", SessionID: "$1", WindowID: "@1"}, name: "codex", providerPID: 101, status: agentStatusWorking}
	second := agentRow{pane: tmux.Pane{PaneID: "%2", PanePID: "200", SessionID: "$2", WindowID: "@2"}, name: "claude", providerPID: 201, status: agentStatusAttention}
	snapshot := agentPreviewSnapshot{Order: []string{"%1", "%2"}, Rows: agentPreviewRows([]agentRow{first, second}, time.Now()), ProviderPIDs: map[string]int{"%1": 101, "%2": 201}}
	ordered := frozenAgentRows(snapshot, []agentRow{second, first})
	if ordered[0].pane.PaneID != "%1" || ordered[1].pane.PaneID != "%2" {
		t.Fatal("polling reordered rows")
	}
	for _, change := range []func(*agentRow){
		func(r *agentRow) { r.pane.PanePID = "999" },
		func(r *agentRow) { r.pane.WindowID = "@9" },
		func(r *agentRow) { r.pane.SessionID = "$9" },
		func(r *agentRow) { r.providerPID = 999 },
		func(r *agentRow) { r.name = "claude" },
	} {
		changed := first
		change(&changed)
		if got := frozenAgentRows(snapshot, []agentRow{second, changed}); got[0].pane.PaneID != "" || got[1].pane.PaneID != "%2" {
			t.Fatalf("changed identity reached a frozen row: %+v", got)
		}
	}
}

func TestPickerStateSurvivesPopupRelaunch(t *testing.T) {
	t.Setenv("TMUX_MENU_PICKER_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	state := &pickerLoopState{Views: map[string]*picker.State{"agents": {Query: "project", Selected: "%7"}, "docs": {Query: "guide"}}, AgentFilter: "attention", SessionOnly: true}
	if err := savePickerLoopState(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPickerLoopState()
	if err != nil || loaded.Views["agents"].Query != "project" || loaded.Views["agents"].Selected != "%7" || loaded.Views["docs"].Query != "guide" || loaded.AgentFilter != "attention" || !loaded.SessionOnly {
		t.Fatalf("restored state = %+v, %v", loaded, err)
	}
}

func TestGitSelectionDetailsAreReadOnlyAndHandleNonRepositories(t *testing.T) {
	root := t.TempDir()
	if got := agentGitBranch(t.Context(), root); got != "—" {
		t.Fatalf("non-repository branch = %q", got)
	}
	cmd := exec.Command("git", "init", "--quiet", "--initial-branch=preview-test", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("initialize fixture: %v: %s", err, out)
	}
	if got := agentGitBranch(t.Context(), root); got != "preview-test" {
		t.Fatalf("branch = %q", got)
	}
	var out bytes.Buffer
	printAgentSelectionDetails(context.Background(), &out, tmux.Pane{SessionName: "work", PaneTitle: "full thread title", CurrentPath: root}, agentPreviewData{Provider: "codex", StateAge: "4m"})
	for _, field := range []string{"Thread: full thread title", "Workdir: " + root, "Git: preview-test", "Session: work · state age: 4m"} {
		if !strings.Contains(out.String(), field) {
			t.Fatalf("missing %q in %q", field, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "index.lock")); !os.IsNotExist(err) {
		t.Fatal("preview left a Git lock behind")
	}
}
