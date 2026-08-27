package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"tmux-menu/internal/action"
	"tmux-menu/internal/agenthud"
	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

func TestAgentHUDRefreshMapsSharedInventoryAndPerPaneCaptureErrors(t *testing.T) {
	oldInventory := loadAgentHUDInventory
	oldCapture := captureAgentHUDPane
	oldList := listAgentHUDPanes
	oldClock := agentHUDClock
	defer func() {
		loadAgentHUDInventory, captureAgentHUDPane, listAgentHUDPanes, agentHUDClock = oldInventory, oldCapture, oldList, oldClock
	}()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	agentHUDClock = func() time.Time { return now }
	rows := []agentRow{
		{
			pane:     tmux.Pane{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "101", PaneTitle: "Build HUD|Working", CurrentPath: "/tmp/tmux-menu"},
			provider: agentstatus.ProviderCodex, providerPID: 201, status: agentStatusWorking, name: "codex", statusSource: "hook", fresh: true,
			turnStartedAt: now.Add(-time.Minute), stateChangedAt: now.Add(-30 * time.Second), lastEventAt: now.Add(-2 * time.Second),
		},
		{
			pane:     tmux.Pane{SessionName: "work", SessionID: "$1", WindowID: "@2", PaneID: "%2", PanePID: "102", PaneTitle: "✳ Review", CurrentPath: "/tmp/review"},
			provider: agentstatus.ProviderClaude, providerPID: 202, status: agentStatusAttention, name: "claude", statusSource: "hook", fresh: true,
		},
	}
	loadAgentHUDInventory = func(_ context.Context, fallback string, gotNow time.Time, budget *tmux.OutputBudget) (agentInventory, error) {
		if fallback != config.DefaultSessionColor || !gotNow.Equal(now) || budget == nil {
			t.Fatalf("inventory args fallback=%q now=%v budget=%v", fallback, gotNow, budget)
		}
		return agentInventory{rows: rows, sessionColors: map[string]string{"$1": "orange"}}, nil
	}
	captureAgentHUDPane = func(_ context.Context, _ *tmux.OutputBudget, paneID string, lines int, maxBytes int64) (string, error) {
		if lines <= 0 || maxBytes <= 0 {
			t.Fatalf("unbounded capture lines=%d bytes=%d", lines, maxBytes)
		}
		if paneID == "%2" {
			return "", errors.New("gone\x1b]52;c;owned\a")
		}
		return "live\x1b[31m output", nil
	}
	listAgentHUDPanes = func(context.Context, *tmux.OutputBudget, tmux.PaneListLimits) ([]tmux.Pane, error) {
		return []tmux.Pane{rows[0].pane, rows[1].pane}, nil
	}

	identity1, err := agenthud.NewIdentity("$1", "@1", "%1", 101, 201)
	if err != nil {
		t.Fatal(err)
	}
	identity2, err := agenthud.NewIdentity("$1", "@2", "%2", 102, 202)
	if err != nil {
		t.Fatal(err)
	}
	refresh := newAgentHUDRefresh(config.Default(), runtimeContext{OriginPane: "%1"}, newAgentHUDCoordinator())
	result, err := refresh(t.Context(), agenthud.RefreshRequest{Generation: 7, Targets: []agenthud.CaptureTarget{
		{Identity: identity1},
		{Identity: identity2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Agents) != 2 || len(result.Captures) != 2 {
		t.Fatalf("refresh = %#v", result)
	}
	if got := result.Agents[0].Identity(); got != identity1 || result.Agents[0].Status() != agentstatus.StateWorking || result.Agents[0].Source().Plain() != "hook" {
		t.Fatalf("first mapped agent = %#v", result.Agents[0])
	}
	if got := result.Captures[0].Terminal.Plain(); got != "live output" {
		t.Fatalf("safe terminal = %q", got)
	}
	if !result.Captures[1].Failed || !strings.Contains(result.Captures[1].Failure.Plain(), "gone") || strings.Contains(result.Captures[1].Failure.Plain(), "owned") {
		t.Fatalf("capture failure was not sanitized: %q", result.Captures[1].Failure.Plain())
	}
}

func TestAgentHUDGenerationBudgetIncludesWorstCaseVisibleCaptures(t *testing.T) {
	wantMinimum := 2*tmux.DefaultPaneListLimits().MaxOutputBytes + agentProcessOutputBytes + int64(agentHUDMaxVisiblePanes*agentHUDCaptureBytes)
	if agentHUDTotalOutputBytes < wantMinimum {
		t.Fatalf("generation output budget = %d, need at least %d for pane inventory, process inventory, and visible captures", agentHUDTotalOutputBytes, wantMinimum)
	}
}

func TestAgentHUDRefreshRejectsCaptureWhenPaneRespawnsDuringCapture(t *testing.T) {
	oldInventory := loadAgentHUDInventory
	oldCapture := captureAgentHUDPane
	oldList := listAgentHUDPanes
	defer func() {
		loadAgentHUDInventory, captureAgentHUDPane, listAgentHUDPanes = oldInventory, oldCapture, oldList
	}()

	pane := tmux.Pane{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "101", CurrentCommand: "codex"}
	loadAgentHUDInventory = func(context.Context, string, time.Time, *tmux.OutputBudget) (agentInventory, error) {
		return agentInventory{rows: []agentRow{{pane: pane, provider: agentstatus.ProviderCodex, providerPID: 201, status: agentStatusWorking}}}, nil
	}
	captureAgentHUDPane = func(context.Context, *tmux.OutputBudget, string, int, int64) (string, error) {
		return "replacement secret", nil
	}
	listAgentHUDPanes = func(context.Context, *tmux.OutputBudget, tmux.PaneListLimits) ([]tmux.Pane, error) {
		reused := pane
		reused.PanePID = "999"
		return []tmux.Pane{reused}, nil
	}
	identity, err := agenthud.NewIdentity("$1", "@1", "%1", 101, 201)
	if err != nil {
		t.Fatal(err)
	}
	result, err := newAgentHUDRefresh(config.Default(), runtimeContext{}, newAgentHUDCoordinator())(t.Context(), agenthud.RefreshRequest{
		Generation: 1, Targets: []agenthud.CaptureTarget{{Identity: identity}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Captures) != 1 || !result.Captures[0].Failed || result.Captures[0].Terminal.Plain() != "" {
		t.Fatalf("respawned pane capture was retained: %#v", result.Captures)
	}
}

func TestAgentHUDHookLookupIsReadOnlyOnIdentityMismatch(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tmuxEnv := "/private/tmp/tmux-test/default,123,0"
	t.Setenv("TMUX_MENU_AGENT_STATE_DIR", stateDir)
	t.Setenv("TMUX", tmuxEnv)
	store, err := openDefaultAgentStatusStore()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	paneIdentity := agentstatus.PaneIdentity{
		ServerID: agentstatus.ServerFingerprint(tmuxEnv), PaneID: "%1", PanePID: 101,
		ProviderPID: 201, TmuxSessionID: "$1",
	}
	_, decision, err := store.Apply(t.Context(), agentstatus.Event{
		Pane: paneIdentity, Provider: agentstatus.ProviderCodex, ProviderSessionID: "session-1", TurnID: "turn-1",
		Kind: agentstatus.EventTurnStart, RawEvent: "UserPromptSubmit", ObservedAt: now,
	})
	if err != nil || !decision.Applied {
		t.Fatalf("apply hook record decision=%+v err=%v", decision, err)
	}
	pane := tmux.Pane{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "101", CurrentCommand: "codex"}
	annotations := loadAgentHookAnnotationsMode(t.Context(), []tmux.Pane{pane}, processSnapshot{available: true}, now.Add(time.Second), true, tmux.NewOutputBudget(1<<20))
	if len(annotations) != 0 {
		t.Fatalf("mismatched hook remained authoritative: %+v", annotations)
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	records := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			records++
		}
	}
	if records != 1 {
		t.Fatalf("read-only HUD lookup retained %d records, want 1", records)
	}
}

func TestAgentHUDProjectionKeepsExactIdentityForDuplicateLabels(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "101", PaneTitle: "same", CurrentPath: "/tmp/same"},
		{SessionName: "work", SessionID: "$1", WindowID: "@2", PaneID: "%2", PanePID: "102", PaneTitle: "same", CurrentPath: "/tmp/same"},
	}
	rows := []agentRow{
		{pane: panes[0], provider: agentstatus.ProviderCodex, providerPID: 201, status: agentStatusWorking, name: "codex"},
		{pane: panes[1], provider: agentstatus.ProviderCodex, providerPID: 202, status: agentStatusWorking, name: "codex"},
	}
	agents, _ := agentHUDProjection(agentInventory{rows: rows}, "")
	if len(agents) != 2 {
		t.Fatalf("duplicate labels collapsed agents: %#v", agents)
	}
	dispatch := agentHUDSwitchDispatch(agents[1].Identity())
	if dispatch.SessionID != "$1" || dispatch.WindowID != "@2" || dispatch.PaneID != "%2" || dispatch.PanePID != 102 || dispatch.ProviderPID != 202 {
		t.Fatalf("second duplicate-label dispatch = %#v", dispatch)
	}
}

func TestAgentHUDRefreshRevalidatesCaptureTargetAgainstFreshIdentity(t *testing.T) {
	oldInventory := loadAgentHUDInventory
	oldCapture := captureAgentHUDPane
	defer func() { loadAgentHUDInventory, captureAgentHUDPane = oldInventory, oldCapture }()
	loadAgentHUDInventory = func(context.Context, string, time.Time, *tmux.OutputBudget) (agentInventory, error) {
		return agentInventory{rows: []agentRow{{
			pane:     tmux.Pane{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "999", CurrentCommand: "codex"},
			provider: agentstatus.ProviderCodex, providerPID: 299, status: agentStatusWorking, fresh: true,
		}}}, nil
	}
	captureAgentHUDPane = func(context.Context, *tmux.OutputBudget, string, int, int64) (string, error) {
		t.Fatal("reused pane identity must not be captured")
		return "", nil
	}
	oldIdentity, err := agenthud.NewIdentity("$1", "@1", "%1", 101, 201)
	if err != nil {
		t.Fatal(err)
	}
	result, err := newAgentHUDRefresh(config.Default(), runtimeContext{}, newAgentHUDCoordinator())(t.Context(), agenthud.RefreshRequest{
		Generation: 1, Targets: []agenthud.CaptureTarget{{Identity: oldIdentity}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Captures) != 1 || !result.Captures[0].Failed {
		t.Fatalf("identity mismatch must become a safe stale update: %#v", result.Captures)
	}
}

func TestHUDPickerCancellationResumesSameModelContext(t *testing.T) {
	oldRun := runAgentHUDProgram
	oldSelect := selectModeForLoop
	defer func() { runAgentHUDProgram, selectModeForLoop = oldRun, oldSelect }()

	model := agenthud.NewModel(agenthud.ModelOptions{Width: 100, Height: 24})
	agents := make([]agenthud.Agent, 5)
	for i := range agents {
		identity, err := agenthud.NewIdentity("$1", "@1", "%"+string(rune('1'+i)), 101+i, 201+i)
		if err != nil {
			t.Fatal(err)
		}
		agents[i] = agenthud.NewAgent(agenthud.AgentPresentation{Identity: identity, Status: agentstatus.StateWorking})
	}
	request := model.BeginRefresh()
	model.ApplyRefresh(agenthud.RefreshResult{Generation: request.Generation, Agents: agents})
	model.HandleKey(agenthud.KeyPageNext)
	model.HandleKey(agenthud.KeyFocus)
	wantPane, wantPage := model.SelectedPaneID(), model.Page()

	calls := 0
	runAgentHUDProgram = func(_ context.Context, got agenthud.Model, _ agenthud.RefreshFunc, _ agenthud.RuntimeOptions) (agenthud.RunResult, error) {
		calls++
		if got.SelectedPaneID() != wantPane || got.Page() != wantPage || !got.Focused() {
			t.Fatalf("HUD context changed on run %d: pane=%q page=%d focus=%v", calls, got.SelectedPaneID(), got.Page(), got.Focused())
		}
		key := agenthud.KeyOpenPicker
		if calls == 2 {
			key = agenthud.KeyQuit
		}
		return agenthud.RunResult{Model: got, Action: got.HandleKey(key)}, nil
	}
	selectModeForLoop = func(_ context.Context, mode, initialPaneID string) (picker.Result[menuItem], error) {
		if mode != "agents" || initialPaneID != wantPane {
			t.Fatalf("picker mode=%q initial=%q", mode, initialPaneID)
		}
		return picker.Result[menuItem]{}, picker.ErrCanceled
	}
	if err := runAgentHUDSession(t.Context(), model, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("HUD runs = %d, want suspend then resume", calls)
	}
}

func TestHUDPickerSelectionDispatchesExactIdentityAndExits(t *testing.T) {
	dispatchPath := t.TempDir() + "/dispatch.json"
	t.Setenv("TMUX_MENU_DISPATCH_FILE", dispatchPath)
	oldSelect := selectModeForLoop
	defer func() { selectModeForLoop = oldSelect }()
	want := action.Dispatch{Mode: "switch-pane", SessionID: "$1", WindowID: "@2", PaneID: "%3", PanePID: 404, ProviderPID: 505}
	selectModeForLoop = func(context.Context, string, string) (picker.Result[menuItem], error) {
		return picker.Result[menuItem]{Selected: true, Value: menuItem{dispatch: want}}, nil
	}
	canceled, err := runHUDPickerSubflow(t.Context(), "%3")
	if err != nil {
		t.Fatal(err)
	}
	if canceled {
		t.Fatal("successful selection must exit the HUD")
	}
	got, err := action.Read(dispatchPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatch = %#v, want %#v", got, want)
	}
}

func TestHUDPickerSubflowPreservesRefreshAcknowledgeAndViewSwitchBehavior(t *testing.T) {
	dispatchPath := t.TempDir() + "/dispatch.json"
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("TMUX_MENU_DISPATCH_FILE", dispatchPath)
	t.Setenv("TMUX_MENU_ORIGIN_PANE", "%1")
	t.Setenv("TMUX_MENU_ORIGIN_PATH", root)
	t.Setenv("TMUX_MENU_SESSION_ID", "$1")
	t.Setenv("TMUX_MENU_SESSION_NAME", "work")
	t.Setenv("TMUX_MENU_SESSION_PATH", root)
	oldSelect := selectModeForLoop
	oldAck := acknowledgeAgent
	defer func() { selectModeForLoop, acknowledgeAgent = oldSelect, oldAck }()

	var initial []string
	selectModeForLoop = func(_ context.Context, mode, initialPaneID string) (picker.Result[menuItem], error) {
		if mode != "agents" {
			t.Fatalf("mode = %q", mode)
		}
		initial = append(initial, initialPaneID)
		switch len(initial) {
		case 1:
			return picker.Result[menuItem]{Key: "ctrl-r", Selected: true, Value: menuItem{agentPaneID: "%7"}}, nil
		case 2:
			return picker.Result[menuItem]{Key: "ctrl-x", Selected: true, Value: menuItem{agentPaneID: "%7", agentAckToken: "cas-token"}}, nil
		default:
			return picker.Result[menuItem]{Key: "alt-3"}, nil
		}
	}
	acknowledged := ""
	acknowledgeAgent = func(_ context.Context, token string) error {
		acknowledged = token
		return nil
	}
	canceled, err := runHUDPickerSubflow(t.Context(), "%1")
	if err != nil {
		t.Fatal(err)
	}
	if canceled {
		t.Fatal("view switch must exit the delegated picker and HUD")
	}
	if want := []string{"%1", "%7", "%7"}; !reflect.DeepEqual(initial, want) {
		t.Fatalf("stable picker selection sequence = %#v, want %#v", initial, want)
	}
	if acknowledged != "cas-token" {
		t.Fatalf("acknowledged = %q", acknowledged)
	}
	dispatched, err := action.Read(dispatchPath)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.Mode != popupViewDispatchMode || dispatched.Cmd != "tools" {
		t.Fatalf("view dispatch = %#v", dispatched)
	}
}
