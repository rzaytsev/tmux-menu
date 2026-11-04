package main

import (
	"os"
	"strconv"
	"strings"
	"time"

	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/programstatus"
	"tmux-menu/internal/tmux"
)

func applyAgentOSC(rows []agentRow, processes processSnapshot, budget *tmux.OutputBudget) {
	if !processes.available {
		return
	}
	store, err := openDefaultAgentStatusStore()
	if err != nil {
		return
	}
	server := agentstatus.ServerFingerprint(os.Getenv("TMUX"))
	if server == "" {
		return
	}
	var consume func(int) bool
	if budget != nil {
		consume = budget.Consume
	}
	for i := range rows {
		row := &rows[i]
		pane := agentPaneIdentity(server, row.pane, processes, row.provider)
		value, err := store.ReadOSC(pane, consume)
		if err != nil || value.Provider != row.provider || !oscOwnerMatches(value, *row, processes) {
			continue
		}
		applyOSCToRow(row, value)
	}
}

func oscOwnerMatches(value agentstatus.OSCSnapshot, row agentRow, processes processSnapshot) bool {
	panePID, _ := strconv.Atoi(row.pane.PanePID)
	providerPID := processes.providerPIDs[panePID][row.provider]
	return processes.available && providerPID > 0 &&
		!strings.ContainsAny(processes.processStates[value.WrapperPID], "TZ") &&
		processes.parents[value.ChildPID] == value.WrapperPID &&
		processDescendsFrom(processes.parents, value.WrapperPID, panePID) &&
		processDescendsFrom(processes.parents, providerPID, value.ChildPID)
}

func applyOSCToRow(row *agentRow, value agentstatus.OSCSnapshot) {
	var selected programstatus.Record
	for _, record := range value.Records {
		if oscStateRank(record.State) > oscStateRank(selected.State) ||
			oscStateRank(record.State) == oscStateRank(selected.State) && record.UpdatedAt.After(selected.UpdatedAt) {
			selected = record
		}
	}
	if selected.State == "" {
		return
	} // clear means return to hook/title evidence.
	// Codex reports idle at completion rather than done. Preserve the exact
	// hook completion token only if it belongs to the most recent OSC work.
	if row.provider == agentstatus.ProviderCodex && selected.State == "idle" && row.status == agentStatusCompleted && !value.LastWorkingAt.IsZero() && !row.lastEventAt.Before(value.LastWorkingAt) {
		return
	}
	row.status = oscAgentState(selected.State)
	row.statusSource = "osc7501"
	row.rawEvent = selected.State
	row.reason = selected.State
	if selected.Kind != "" {
		row.reason += ":" + selected.Kind
	}
	row.updatedAt, row.lastEventAt, row.stateChangedAt = selected.UpdatedAt, selected.UpdatedAt, selected.UpdatedAt
	row.turnStartedAt = value.LastWorkingAt
	row.fresh = true
	row.acknowledgeToken = "" // native done clears when the application reports idle.
	row.expiredHookReason, row.expiredHookEventAt = "", time.Time{}
	for _, record := range value.Records {
		if record.ID == "" {
			continue
		}
		row.children = append(row.children, agentstatus.ChildAnnotation{ID: "osc:" + record.ID, Type: "osc7501", State: oscAgentState(record.State), Reason: record.Kind, UpdatedAt: record.UpdatedAt, LastEventAt: record.UpdatedAt, Fresh: true})
	}
}

func oscStateRank(state string) int {
	switch state {
	case "blocked":
		return 5
	case "error":
		return 4
	case "working":
		return 3
	case "done":
		return 2
	case "idle":
		return 1
	}
	return 0
}
func oscAgentState(state string) agentStatus {
	switch state {
	case "blocked", "error":
		return agentStatusAttention
	case "working":
		return agentStatusWorking
	case "done":
		return agentStatusCompleted
	case "idle":
		return agentStatusWaiting
	}
	return agentStatusUnknown
}
