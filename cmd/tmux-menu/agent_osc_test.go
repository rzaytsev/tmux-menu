package main

import (
	"testing"
	"time"

	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/programstatus"
	"tmux-menu/internal/tmux"
)

func TestOSCOwnershipRequiresLiveWrapperAndProviderDescendant(t *testing.T) {
	row := agentRow{pane: tmux.Pane{PanePID: "100"}, provider: agentstatus.ProviderClaude}
	value := agentstatus.OSCSnapshot{WrapperPID: 101, ChildPID: 102}
	snapshot := processSnapshot{available: true, parents: map[int]int{101: 100, 102: 101, 103: 102}, providerPIDs: map[int]map[agentstatus.Provider]int{100: {agentstatus.ProviderClaude: 103}}}
	if !oscOwnerMatches(value, row, snapshot) {
		t.Fatal("rejected exact live tree")
	}
	for _, parents := range []map[int]int{{102: 101, 103: 102}, {101: 999, 102: 101, 103: 102}, {101: 100, 102: 101, 103: 100}, {101: 100, 102: 999, 103: 102}} {
		snapshot.parents = parents
		if oscOwnerMatches(value, row, snapshot) {
			t.Fatalf("accepted stale/unrelated tree: %+v", parents)
		}
	}
}

func TestOSCOverridesTitleAndHooksWithoutAQuietTimeLease(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	row := agentRow{status: agentStatusWorking, statusSource: "hook", fresh: false, acknowledgeToken: "old", expiredHookReason: "old"}
	value := agentstatus.OSCSnapshot{UpdatedAt: old, Records: []programstatus.Record{{State: "idle", UpdatedAt: old}, {ID: "worker", State: "blocked", Kind: "permission", UpdatedAt: old}}}
	applyOSCToRow(&row, value)
	if row.status != agentStatusAttention || row.statusSource != "osc7501" || !row.fresh || row.reason != "blocked:permission" || row.acknowledgeToken != "" || row.expiredHookReason != "" {
		t.Fatalf("OSC projection: %+v", row)
	}
	if len(row.children) != 1 {
		t.Fatal("missing OSC child")
	}
	for native, want := range map[string]agentStatus{"working": agentStatusWorking, "idle": agentStatusWaiting, "done": agentStatusCompleted, "error": agentStatusAttention} {
		value.Records = []programstatus.Record{{State: native, UpdatedAt: old}}
		applyOSCToRow(&row, value)
		if row.status != want {
			t.Fatalf("state %s: %s", native, row.status)
		}
	}
}

func TestOSCClearFallsBackAndCodexIdleRetainsCurrentHookCompletion(t *testing.T) {
	now := time.Now()
	row := agentRow{provider: agentstatus.ProviderCodex, status: agentStatusCompleted, statusSource: "hook", lastEventAt: now, acknowledgeToken: "exact"}
	value := agentstatus.OSCSnapshot{LastWorkingAt: now.Add(-time.Second), Records: []programstatus.Record{{State: "idle", UpdatedAt: now}}}
	applyOSCToRow(&row, value)
	if row.acknowledgeToken != "exact" || row.statusSource != "hook" {
		t.Fatal("lost completion")
	}
	value.LastWorkingAt = now.Add(time.Second)
	applyOSCToRow(&row, value)
	if row.status != agentStatusWaiting || row.statusSource != "osc7501" {
		t.Fatal("old completion leaked into next work")
	}
	row = agentRow{status: agentStatusAttention, statusSource: "hook"}
	applyOSCToRow(&row, agentstatus.OSCSnapshot{})
	if row.status != agentStatusAttention || row.statusSource != "hook" {
		t.Fatal("clear should fall back")
	}
}

func TestProcessAncestryCyclesAndDepthAreBounded(t *testing.T) {
	if processDescendsFrom(map[int]int{1: 2, 2: 1}, 1, 99) {
		t.Fatal("accepted cycle")
	}
}

func TestClaudeNativeIdleOverridesHookCompletion(t *testing.T) {
	now := time.Now()
	row := agentRow{provider: agentstatus.ProviderClaude, status: agentStatusCompleted, statusSource: "hook", lastEventAt: now, acknowledgeToken: "old"}
	applyOSCToRow(&row, agentstatus.OSCSnapshot{LastWorkingAt: now.Add(-time.Second), Records: []programstatus.Record{{State: "idle", UpdatedAt: now}}})
	if row.status != agentStatusWaiting || row.statusSource != "osc7501" || row.acknowledgeToken != "" {
		t.Fatalf("Claude idle kept a hook completion: %+v", row)
	}
}
