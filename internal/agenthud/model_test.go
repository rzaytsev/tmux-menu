package agenthud

import (
	"testing"
	"time"

	"tmux-menu/internal/agentstatus"
)

func TestRefreshUpdatesTailWithoutReorderingStatusChanges(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	model := NewModel(ModelOptions{Width: 120, Height: 30, Now: now, MaxTerminalBytes: 4096})
	first := model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{
		Generation: first.Generation,
		Now:        now,
		Agents: []Agent{
			testAgent(t, "%1", agentstatus.StateWorking),
			testAgent(t, "%2", agentstatus.StateWaiting),
			testAgent(t, "%3", agentstatus.StateWorking),
			testAgent(t, "%4", agentstatus.StateCompleted),
		},
		Captures: []TerminalUpdate{testTail(t, "%1", "old")},
	})
	wantSlots := model.SlotPaneIDs()

	second := model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{
		Generation: second.Generation,
		Now:        now.Add(time.Second),
		Agents: []Agent{
			testAgent(t, "%1", agentstatus.StateAttention),
			testAgent(t, "%2", agentstatus.StateWaiting),
			testAgent(t, "%3", agentstatus.StateWorking),
			testAgent(t, "%4", agentstatus.StateCompleted),
		},
		Captures: []TerminalUpdate{testTail(t, "%1", "new")},
	})
	if got := model.SlotPaneIDs(); !equalStrings(got, wantSlots) {
		t.Fatalf("status refresh reordered slots: got %v want %v", got, wantSlots)
	}
	if got, ok := model.TerminalPlain("%1"); !ok || got != "new" {
		t.Fatalf("refreshed tail = %q, %v", got, ok)
	}
}

func TestPagingSummaryAndNextAttentionCrossPages(t *testing.T) {
	model := NewModel(ModelOptions{Width: 120, Height: 30, MaxTerminalBytes: 4096})
	agents := []Agent{
		testAgent(t, "%1", agentstatus.StateWorking),
		testAgent(t, "%2", agentstatus.StateWaiting),
		testAgent(t, "%3", agentstatus.StateCompleted),
		testAgent(t, "%4", agentstatus.StateUnknown),
		testAgent(t, "%5", agentstatus.StateAttention),
	}
	applyAgents(t, &model, agents)
	if model.PageCount() != 2 || model.Summary().Attention != 1 || model.Summary().Total != 5 {
		t.Fatalf("page/summary = %d %#v", model.PageCount(), model.Summary())
	}
	if action := model.HandleKey(KeyNextAttention); action.Kind() != ActionNone {
		t.Fatalf("navigation emitted action %v", action.Kind())
	}
	if model.SelectedPaneID() != "%5" || model.Page() != 1 {
		t.Fatalf("next attention selected=%q page=%d", model.SelectedPaneID(), model.Page())
	}
	if action := model.HandleKey(KeyPagePrevious); action.Kind() != ActionNone || model.Page() != 0 {
		t.Fatalf("page previous action=%v page=%d", action.Kind(), model.Page())
	}
}

func TestFocusSurvivesUnrelatedChurnAndExitsWhenTargetDisappears(t *testing.T) {
	model := NewModel(ModelOptions{Width: 120, Height: 30})
	applyAgents(t, &model, []Agent{testAgent(t, "%1", agentstatus.StateWorking), testAgent(t, "%2", agentstatus.StateWaiting)})
	model.HandleKey(KeyRight)
	model.HandleKey(KeyFocus)
	if !model.Focused() || model.SelectedPaneID() != "%2" {
		t.Fatalf("focus selection = %q focused=%v", model.SelectedPaneID(), model.Focused())
	}
	applyAgents(t, &model, []Agent{testAgent(t, "%2", agentstatus.StateAttention), testAgent(t, "%3", agentstatus.StateWorking)})
	if !model.Focused() || model.SelectedPaneID() != "%2" {
		t.Fatalf("focus did not survive: selected=%q focused=%v", model.SelectedPaneID(), model.Focused())
	}
	applyAgents(t, &model, []Agent{testAgent(t, "%3", agentstatus.StateWorking)})
	if model.Focused() || model.SelectedPaneID() != "%3" {
		t.Fatalf("vanished focus = selected=%q focused=%v", model.SelectedPaneID(), model.Focused())
	}
}

func TestEmptyModelKeepsRefreshingAndAdmitsAgent(t *testing.T) {
	model := NewModel(ModelOptions{Width: 80, Height: 20})
	first := model.BeginRefresh()
	if len(first.Targets) != 0 {
		t.Fatalf("empty model capture targets = %v", first.Targets)
	}
	if !model.ApplyRefresh(RefreshResult{Generation: first.Generation}) || model.AgentCount() != 0 {
		t.Fatal("empty refresh was not accepted")
	}
	second := model.BeginRefresh()
	if !model.ApplyRefresh(RefreshResult{Generation: second.Generation, Agents: []Agent{testAgent(t, "%9", agentstatus.StateWorking)}}) {
		t.Fatal("agent refresh was not accepted")
	}
	if model.SelectedPaneID() != "%9" || model.AgentCount() != 1 {
		t.Fatalf("new agent selected=%q count=%d", model.SelectedPaneID(), model.AgentCount())
	}
}

func TestCompletedEmptyAndFailedCapturesWaitForNormalRefresh(t *testing.T) {
	agent := testAgent(t, "%1", agentstatus.StateWorking)

	for _, tc := range []struct {
		name   string
		update TerminalUpdate
	}{
		{name: "empty", update: TerminalUpdate{Identity: agent.Identity()}},
		{name: "failed", update: TerminalUpdate{Identity: agent.Identity(), Failed: true, Failure: SanitizeText("pane vanished", 40)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := NewModel(ModelOptions{Width: 80, Height: 20})
			applyAgents(t, &model, []Agent{agent})
			if !model.NeedsVisibleCapture() {
				t.Fatal("newly visible pane did not request an immediate capture")
			}
			request := model.BeginRefresh()
			model.ApplyRefresh(RefreshResult{Generation: request.Generation, Agents: []Agent{agent}, Captures: []TerminalUpdate{tc.update}})
			if model.NeedsVisibleCapture() {
				t.Fatal("completed capture attempt would start a zero-delay refresh loop")
			}
		})
	}
}

func TestPaneReincarnationDropsPriorTailAndRequestsFreshCapture(t *testing.T) {
	model := NewModel(ModelOptions{Width: 80, Height: 20, MaxTerminalBytes: 4096})
	oldAgent := testAgent(t, "%1", agentstatus.StateWorking)
	request := model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{
		Generation: request.Generation,
		Agents:     []Agent{oldAgent},
		Captures:   []TerminalUpdate{testTail(t, "%1", "old incarnation")},
	})

	identity, err := NewIdentity("$1", "@1", "%1", 999, 1001)
	if err != nil {
		t.Fatal(err)
	}
	newAgent := NewAgent(AgentPresentation{Identity: identity, Status: agentstatus.StateWorking})
	request = model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{Generation: request.Generation, Agents: []Agent{newAgent}})

	if got, ok := model.TerminalPlain("%1"); ok || got != "" {
		t.Fatalf("reincarnated pane retained terminal tail %q, ok=%v", got, ok)
	}
	if !model.NeedsVisibleCapture() {
		t.Fatal("reincarnated pane did not request a fresh capture")
	}
}

func TestRefreshFailureKeepsLastGoodSafeTailAndLateGenerationIsRejected(t *testing.T) {
	model := NewModel(ModelOptions{Width: 80, Height: 20, MaxTerminalBytes: 4096})
	first := model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{Generation: first.Generation, Agents: []Agent{testAgent(t, "%1", agentstatus.StateWorking)}, Captures: []TerminalUpdate{testTail(t, "%1", "good")}})
	late := model.BeginRefresh()
	current := model.BeginRefresh()
	if model.ApplyRefresh(RefreshResult{Generation: late.Generation, Agents: []Agent{testAgent(t, "%1", agentstatus.StateAttention)}, Captures: []TerminalUpdate{testTail(t, "%1", "late")}}) {
		t.Fatal("late refresh was accepted")
	}
	failure := SanitizeText("capture failed\x1b]52;c;bad\a", 80)
	if !model.ApplyRefresh(RefreshResult{Generation: current.Generation, Failed: true, Failure: failure}) {
		t.Fatal("current failure was rejected")
	}
	if got, ok := model.TerminalPlain("%1"); !ok || got != "good" || !model.TailStale("%1") {
		t.Fatalf("last-good tail=%q ok=%v stale=%v", got, ok, model.TailStale("%1"))
	}
}

func TestPageCyclingAndFocusImmediatelyEvictHiddenAndRemovedTails(t *testing.T) {
	model := NewModel(ModelOptions{Width: 120, Height: 30, MaxTerminalBytes: 16})
	agents := make([]Agent, 0, 9)
	for i := 1; i <= 9; i++ {
		agents = append(agents, testAgent(t, "%"+string(rune('0'+i)), agentstatus.StateWorking))
	}
	first := model.BeginRefresh()
	updates := []TerminalUpdate{testTail(t, "%1", "1111"), testTail(t, "%2", "2222"), testTail(t, "%3", "3333"), testTail(t, "%4", "4444")}
	model.ApplyRefresh(RefreshResult{Generation: first.Generation, Agents: agents, Captures: updates})
	if model.TailCount() != 4 || model.RetainedTerminalBytes() > 16 {
		t.Fatalf("first page tails=%d bytes=%d", model.TailCount(), model.RetainedTerminalBytes())
	}
	model.HandleKey(KeyPageNext)
	if model.TailCount() != 0 {
		t.Fatalf("hidden page tails retained: %d", model.TailCount())
	}
	second := model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{Generation: second.Generation, Agents: agents, Captures: []TerminalUpdate{
		testTail(t, "%5", "5555"), testTail(t, "%6", "6666"), testTail(t, "%7", "7777"), testTail(t, "%8", "8888"),
	}})
	model.HandleKey(KeyFocus)
	if model.TailCount() != 1 || model.RetainedTerminalBytes() > 4 {
		t.Fatalf("focus retained tails=%d bytes=%d", model.TailCount(), model.RetainedTerminalBytes())
	}
	third := model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{Generation: third.Generation, Agents: agents[8:]})
	if model.TailCount() != 0 || model.SelectedPaneID() != "%9" {
		t.Fatalf("removed IDs retained tail=%d selected=%q", model.TailCount(), model.SelectedPaneID())
	}
}

func TestPureKeysReturnExplicitReadOnlyActions(t *testing.T) {
	model := NewModel(ModelOptions{Width: 80, Height: 20})
	applyAgents(t, &model, []Agent{testAgent(t, "%1", agentstatus.StateWorking)})
	if got := model.HandleKey(KeyOpenPicker); got.Kind() != ActionOpenPicker {
		t.Fatalf("picker action = %v", got.Kind())
	}
	if got := model.HandleKey(KeyEnter); got.Kind() != ActionSwitch || got.Identity().PaneID() != "%1" {
		t.Fatalf("switch action = %#v", got)
	}
	if got := model.HandleKey(KeyQuit); got.Kind() != ActionQuit {
		t.Fatalf("quit action = %v", got.Kind())
	}
	for _, key := range []Key{KeyLeft, KeyRight, KeyUp, KeyDown, KeyPageNext, KeyPagePrevious, KeyNextAttention, KeyFocus, KeyHelp} {
		if got := model.HandleKey(key); got.Kind() != ActionNone {
			t.Fatalf("read-only key %q emitted %v", key, got.Kind())
		}
	}
}

func TestGridMovementUsesActualTwoByTwoPositions(t *testing.T) {
	model := NewModel(ModelOptions{Width: 120, Height: 30})
	applyAgents(t, &model, []Agent{
		testAgent(t, "%1", agentstatus.StateWorking), testAgent(t, "%2", agentstatus.StateWorking),
		testAgent(t, "%3", agentstatus.StateWorking), testAgent(t, "%4", agentstatus.StateWorking),
	})
	for _, step := range []struct {
		key  Key
		want string
	}{{KeyRight, "%2"}, {KeyDown, "%4"}, {Key("h"), "%3"}, {Key("k"), "%1"}, {Key("l"), "%2"}, {Key("j"), "%4"}} {
		if action := model.HandleKey(step.key); action.Kind() != ActionNone || model.SelectedPaneID() != step.want {
			t.Fatalf("key %q selected=%q action=%v, want %q", step.key, model.SelectedPaneID(), action.Kind(), step.want)
		}
	}
}

func TestReconcileRejectsInvalidIdentityAndRetainsParityMetadata(t *testing.T) {
	model := NewModel(ModelOptions{Width: 80, Height: 20})
	valid := testAgent(t, "%1", agentstatus.StateWorking)
	valid = NewAgent(AgentPresentation{
		Identity: valid.Identity(), Provider: SanitizeText("codex", 20), Status: agentstatus.StateWorking,
		Source: SanitizeText("terminal-title", 32), Fresh: true,
	})
	invalid := NewAgent(AgentPresentation{Identity: Identity{}, Status: agentstatus.StateAttention})
	applyAgents(t, &model, []Agent{invalid, valid})
	if model.AgentCount() != 1 || model.SelectedPaneID() != "%1" {
		t.Fatalf("invalid identity entered model: slots=%v", model.SlotPaneIDs())
	}
	got := model.agents["%1"]
	if got.Source().Plain() != "terminal-title" || !got.Fresh() {
		t.Fatalf("parity metadata source=%q fresh=%v", got.Source().Plain(), got.Fresh())
	}
}

func testAgent(t *testing.T, paneID string, state agentstatus.State) Agent {
	t.Helper()
	identity, err := NewIdentity("$1", "@1", paneID, 100, 101)
	if err != nil {
		t.Fatal(err)
	}
	return NewAgent(AgentPresentation{
		Identity:     identity,
		ProviderKind: agentstatus.ProviderCodex,
		Provider:     SanitizeText("codex", 20),
		Status:       state,
		Session:      SanitizeText("work", 20),
		Thread:       SanitizeText("thread "+paneID, 40),
		Workdir:      SanitizeText("project", 40),
	})
}

func testTail(t *testing.T, paneID, value string) TerminalUpdate {
	t.Helper()
	identity, err := NewIdentity("$1", "@1", paneID, 100, 101)
	if err != nil {
		t.Fatal(err)
	}
	return TerminalUpdate{Identity: identity, Terminal: SanitizeTerminal([]byte(value), TerminalLimits{Width: 80, Height: 10, MaxInputBytes: 4096, MaxRetainedBytes: 4096})}
}

func applyAgents(t *testing.T, model *Model, agents []Agent) {
	t.Helper()
	request := model.BeginRefresh()
	if !model.ApplyRefresh(RefreshResult{Generation: request.Generation, Agents: agents}) {
		t.Fatalf("generation %d was rejected", request.Generation)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
