package main

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	"tmux-menu/internal/action"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

type agentStatus string

const (
	agentStatusAttention agentStatus = "attention"
	agentStatusWaiting   agentStatus = "waiting"
	agentStatusWorking   agentStatus = "working"
	agentStatusUnknown   agentStatus = "unknown"
)

func selectAgents(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	items := agentItems(panes, rt.OriginPane)
	return picker.SelectWithExpect(ctx, "agents> ", items, viewSwitchKeys, viewSwitchHeaderForConfig(cfg))
}

func agentItems(panes []tmux.Pane, currentPaneID string) []picker.Item[menuItem] {
	snapshot := loadAgentProcessSnapshot(panes)
	return agentItemsWithProcessSnapshot(panes, currentPaneID, snapshot)
}

func agentItemsWithProcessSnapshot(panes []tmux.Pane, currentPaneID string, snapshot processSnapshot) []picker.Item[menuItem] {
	agents := agentPanesWithProcessSnapshot(panes, snapshot)
	items := make([]picker.Item[menuItem], 0, len(agents))
	for _, p := range agents {
		items = append(items, picker.Item[menuItem]{
			Label: agentPaneLabel(p, currentPaneID, agentPaneStatus(p, snapshot), agentPaneName(p, snapshot)),
			Value: menuItem{dispatch: action.SwitchPane(p)},
		})
	}
	return items
}

func agentPanes(panes []tmux.Pane) []tmux.Pane {
	return agentPanesWithProcessSnapshot(panes, loadAgentProcessSnapshot(panes))
}

func agentPanesWithProcessSnapshot(panes []tmux.Pane, snapshot processSnapshot) []tmux.Pane {
	agents := make([]tmux.Pane, 0)
	for _, p := range panes {
		if isDirectAgentPane(p) || isProcessTreeAgentPane(p, snapshot) {
			agents = append(agents, p)
		}
	}
	return agents
}

func isDirectAgentPane(p tmux.Pane) bool {
	return commandClass(p.CurrentCommand) == "agent" || commandClass(p.PaneTitle) == "agent"
}

func isProcessTreeAgentPane(p tmux.Pane, snapshot processSnapshot) bool {
	if len(snapshot.roots) == 0 || p.PanePID == "" {
		return false
	}
	pid, err := strconv.Atoi(p.PanePID)
	return err == nil && snapshot.roots[pid]
}

func loadAgentProcessSnapshot(panes []tmux.Pane) processSnapshot {
	for _, p := range panes {
		if p.PanePID != "" {
			return agentProcessSnapshot()
		}
	}
	return processSnapshot{}
}

func agentPaneStatus(p tmux.Pane, snapshot processSnapshot) agentStatus {
	if status, ok := codexPaneTitleStatus(p); ok {
		return status
	}
	if status, ok := claudeStatusFromPaneTitle(p.PaneTitle); ok {
		return status
	}
	return processAgentPaneStatus(p, snapshot)
}

func processAgentPaneStatus(p tmux.Pane, snapshot processSnapshot) agentStatus {
	if p.PanePID == "" {
		return agentStatusUnknown
	}
	pid, err := strconv.Atoi(p.PanePID)
	if err != nil {
		return agentStatusUnknown
	}
	if status := snapshot.statuses[pid]; status != "" {
		return status
	}
	return agentStatusUnknown
}

func agentPaneName(p tmux.Pane, snapshot processSnapshot) string {
	if name := knownAgentName(p.CurrentCommand); name != "" {
		return name
	}
	if name := knownAgentName(p.PaneTitle); name != "" {
		return name
	}
	pid, err := strconv.Atoi(p.PanePID)
	if err == nil && snapshot.names[pid] != "" {
		return snapshot.names[pid]
	}
	if _, ok := codexPaneTitleInfoFromPane(p); ok {
		return "codex"
	}
	if _, ok := claudeStatusFromPaneTitle(p.PaneTitle); ok {
		return "claude"
	}
	return "agent"
}

type codexPaneTitleInfo struct {
	currentDir  string
	threadTitle string
	runState    string
}

func codexPaneTitleStatus(p tmux.Pane) (agentStatus, bool) {
	if info, ok := codexPaneTitleInfoFromPane(p); ok {
		return codexRunStateStatus(info.runState)
	}
	return "", false
}

func codexPaneTitleInfoFromPane(p tmux.Pane) (codexPaneTitleInfo, bool) {
	if info, ok := parseCodexPaneTitle(p.PaneTitle); ok {
		return info, true
	}
	if p.WindowName != p.PaneTitle {
		return parseCodexPaneTitle(p.WindowName)
	}
	return codexPaneTitleInfo{}, false
}

func codexStatusFromPaneTitle(title string) (agentStatus, bool) {
	info, ok := parseCodexPaneTitle(title)
	if !ok {
		return "", false
	}
	return codexRunStateStatus(info.runState)
}

func parseCodexPaneTitle(title string) (codexPaneTitleInfo, bool) {
	parts := strings.Split(title, "|")
	if len(parts) == 1 {
		state := strings.TrimSpace(parts[0])
		if _, ok := codexRunStateStatus(state); !ok {
			return codexPaneTitleInfo{}, false
		}
		return codexPaneTitleInfo{runState: state}, true
	}
	state := strings.TrimSpace(parts[len(parts)-1])
	if _, ok := codexRunStateStatus(state); !ok {
		if status, ok := codexSpinnerOnlyStatus(parts[0]); ok {
			return codexPaneTitleInfo{
				threadTitle: strings.TrimSpace(strings.Join(parts[1:], "|")),
				runState:    codexRunStateFromStatus(status),
			}, true
		}
		return codexPaneTitleInfo{}, false
	}
	first := strings.TrimSpace(parts[0])
	info := codexPaneTitleInfo{
		currentDir: first,
		runState:   state,
	}
	if title, ok := codexTitleAfterLeadingMarker(first); ok {
		info.currentDir = ""
		info.threadTitle = title
	}
	if len(parts) > 2 {
		info.threadTitle = strings.TrimSpace(strings.Join(parts[1:len(parts)-1], "|"))
	}
	return info, true
}

func codexRunStateStatus(state string) (agentStatus, bool) {
	state = normalizeCodexRunState(state)
	switch state {
	case "Action Required":
		return agentStatusAttention, true
	case "Working", "Thinking":
		return agentStatusWorking, true
	case "Ready":
		return agentStatusWaiting, true
	default:
		return "", false
	}
}

func codexSpinnerOnlyStatus(marker string) (agentStatus, bool) {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return agentStatusWaiting, true
	}
	if containsBraille(marker) {
		return agentStatusWorking, true
	}
	switch marker {
	case ".", "\u00b7", "\u2219", "\u2022":
		return agentStatusWorking, true
	default:
		return "", false
	}
}

func codexTitleAfterLeadingMarker(title string) (string, bool) {
	fields := strings.Fields(title)
	if len(fields) < 2 {
		return "", false
	}
	if _, ok := codexSpinnerOnlyStatus(fields[0]); !ok {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(title, fields[0])), true
}

func codexRunStateFromStatus(status agentStatus) string {
	switch status {
	case agentStatusWorking:
		return "Working"
	case agentStatusAttention:
		return "Action Required"
	default:
		return "Ready"
	}
}

func normalizeCodexRunState(state string) string {
	state = strings.TrimSpace(state)
	if state == "Ready" || state == "Working" || state == "Thinking" || state == "Action Required" {
		return state
	}
	fields := strings.Fields(state)
	for len(fields) > 0 && !hasASCIIAlnum(fields[0]) {
		fields = fields[1:]
	}
	normalized := strings.Join(fields, " ")
	if normalized != "" {
		return normalized
	}
	return state
}

func hasASCIIAlnum(s string) bool {
	for _, r := range s {
		if ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			return true
		}
	}
	return false
}

func claudeStatusFromPaneTitle(title string) (agentStatus, bool) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", false
	}
	lower := strings.ToLower(title)
	if strings.Contains(lower, "action required") ||
		strings.Contains(lower, "needs input") ||
		strings.Contains(lower, "permission required") ||
		strings.Contains(lower, "permission request") ||
		strings.Contains(lower, "permission prompt") {
		return agentStatusAttention, true
	}

	fields := strings.Fields(title)
	if len(fields) == 0 {
		return "", false
	}
	marker := fields[0]
	if claudeWorkingTitleMarker(marker) {
		return agentStatusWorking, true
	}
	if claudeWaitingTitleMarker(marker) {
		return agentStatusWaiting, true
	}
	return "", false
}

func claudeWorkingTitleMarker(marker string) bool {
	if containsBraille(marker) {
		return true
	}
	switch marker {
	case "\u273d", "\u00b7|\u00b7", "\u2219|\u2219":
		return true
	default:
		return false
	}
}

func claudeWaitingTitleMarker(marker string) bool {
	switch marker {
	case "\u2733", "\u273b", "\u2722":
		return true
	default:
		return false
	}
}

func containsBraille(s string) bool {
	for _, r := range s {
		if 0x2800 <= r && r <= 0x28ff {
			return true
		}
	}
	return false
}

type processInfo struct {
	pid     int
	ppid    int
	state   string
	command string
}

type processSnapshot struct {
	roots    map[int]bool
	statuses map[int]agentStatus
	names    map[int]string
}

func agentProcessSnapshot() processSnapshot {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,state=,command=").Output()
	if err != nil {
		return processSnapshot{}
	}
	return buildProcessSnapshot(parseProcessList(string(out)))
}

func buildProcessSnapshot(processes []processInfo) processSnapshot {
	children := make(map[int][]int)
	agents := make(map[int]string)
	processByPID := make(map[int]processInfo)
	for _, p := range processes {
		processByPID[p.pid] = p
		children[p.ppid] = append(children[p.ppid], p.pid)
		if name := processAgentName(p.command); name != "" {
			agents[p.pid] = name
		}
	}
	roots := make(map[int]bool)
	statuses := make(map[int]agentStatus)
	names := make(map[int]string)
	visiting := make(map[int]bool)
	var scan func(int) (bool, bool, string)
	scan = func(pid int) (bool, bool, string) {
		if roots[pid] {
			return true, statuses[pid] == agentStatusWorking, names[pid]
		}
		if name := agents[pid]; name != "" {
			roots[pid] = true
			names[pid] = name
			working := processLooksRunning(processByPID[pid])
			if working {
				statuses[pid] = agentStatusWorking
			} else {
				statuses[pid] = agentStatusWaiting
			}
			return true, working, name
		}
		if visiting[pid] {
			return false, false, ""
		}
		visiting[pid] = true
		defer delete(visiting, pid)
		found := false
		working := false
		name := ""
		for _, child := range children[pid] {
			childFound, childWorking, childName := scan(child)
			if childFound {
				found = true
				working = working || childWorking
				if name == "" {
					name = childName
				}
			}
		}
		if found {
			roots[pid] = true
			names[pid] = name
			if working {
				statuses[pid] = agentStatusWorking
			} else {
				statuses[pid] = agentStatusWaiting
			}
		}
		return found, working, name
	}
	for _, p := range processes {
		scan(p.pid)
	}
	return processSnapshot{roots: roots, statuses: statuses, names: names}
}

func processLooksRunning(p processInfo) bool {
	return strings.HasPrefix(p.state, "R")
}

func parseProcessList(out string) []processInfo {
	processes := make([]processInfo, 0)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		processes = append(processes, processInfo{
			pid:     pid,
			ppid:    ppid,
			state:   fields[2],
			command: strings.Join(fields[3:], " "),
		})
	}
	return processes
}
