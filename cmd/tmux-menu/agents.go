package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"tmux-menu/internal/action"
	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

type agentStatus = agentstatus.State

const (
	agentStatusAttention            = agentstatus.StateAttention
	agentStatusWaiting              = agentstatus.StateWaiting
	agentStatusWorking              = agentstatus.StateWorking
	agentStatusCompleted            = agentstatus.StateCompleted
	agentStatusUnknown              = agentstatus.StateUnknown
	agentVisibleResultRows          = 12
	agentPickerNonResultRows        = 4
	agentInventoryOutputBytes int64 = 8 << 20
	agentProcessOutputBytes   int64 = 4 << 20
	agentProcessRows                = 32768
	agentProcessFieldBytes          = 16 << 10
)

type agentRow struct {
	pane             tmux.Pane
	provider         agentstatus.Provider
	providerPID      int
	providerSession  string
	status           agentStatus
	name             string
	statusSource     string
	rawEvent         string
	reason           string
	updatedAt        time.Time
	turnStartedAt    time.Time
	stateChangedAt   time.Time
	lastEventAt      time.Time
	fresh            bool
	children         []agentstatus.ChildAnnotation
	acknowledgeToken string
}

type agentInventory struct {
	rows          []agentRow
	sessionColors map[string]string
}

func selectAgentsAt(ctx context.Context, initialPaneID string) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	now := time.Now()
	inventory, err := loadAgentInventory(ctx, cfg.Session.Color, now)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	rows := agentRowsForPicker(inventory.rows)
	items := agentItemsForRows(rows, rt.OriginPane, inventory.sessionColors, cfg.Agents, true)
	executable, err := os.Executable()
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	previewPath, cleanupPreview, err := writeAgentPreviewSnapshot(agentPreviewRows(rows, now))
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	defer cleanupPreview()
	previewCommand := agentPreviewCommandForSnapshot(executable, previewPath)
	keys := append(append([]string(nil), viewSwitchKeys...), "ctrl-r", "ctrl-x")
	return picker.SelectWithExpectAndPreviewOptions(ctx, "agents> ", items, keys, "", previewCommand, picker.Options{
		PreviewWindow: agentPreviewWindow(agentTerminalRows()),
		InitialIndex:  agentInitialIndex(items, initialPaneID),
	})
}

func loadAgentInventory(ctx context.Context, fallbackSessionColor string, now time.Time) (agentInventory, error) {
	return loadAgentInventoryBounded(ctx, fallbackSessionColor, now, tmux.NewOutputBudget(agentInventoryOutputBytes))
}

func loadAgentInventoryBounded(ctx context.Context, fallbackSessionColor string, now time.Time, budget *tmux.OutputBudget) (agentInventory, error) {
	panes, err := tmux.ListPanesBounded(ctx, budget, tmux.DefaultPaneListLimits())
	if err != nil {
		return agentInventory{}, err
	}
	return agentInventoryForPanesBounded(ctx, panes, fallbackSessionColor, now, budget)
}

func loadAgentInventoryBoundedReadOnly(ctx context.Context, fallbackSessionColor string, now time.Time, budget *tmux.OutputBudget) (agentInventory, error) {
	panes, err := tmux.ListPanesBounded(ctx, budget, tmux.DefaultPaneListLimits())
	if err != nil {
		return agentInventory{}, err
	}
	return agentInventoryForPanesBoundedMode(ctx, panes, fallbackSessionColor, now, budget, true)
}

func agentInventoryForPanes(ctx context.Context, panes []tmux.Pane, fallbackSessionColor string, now time.Time) (agentInventory, error) {
	return agentInventoryForPanesBounded(ctx, panes, fallbackSessionColor, now, tmux.NewOutputBudget(agentProcessOutputBytes))
}

func agentInventoryForPanesBounded(ctx context.Context, panes []tmux.Pane, fallbackSessionColor string, now time.Time, budget *tmux.OutputBudget) (agentInventory, error) {
	return agentInventoryForPanesBoundedMode(ctx, panes, fallbackSessionColor, now, budget, false)
}

func agentInventoryForPanesBoundedMode(ctx context.Context, panes []tmux.Pane, fallbackSessionColor string, now time.Time, budget *tmux.OutputBudget, readOnly bool) (agentInventory, error) {
	panes = validLiveAgentPanes(panes)
	snapshot := loadAgentProcessSnapshotBounded(ctx, panes, budget)
	annotations := loadAgentHookAnnotationsMode(ctx, panes, snapshot, now, readOnly, budget)
	rows := agentRowsForPanes(panes, snapshot, annotations)
	var sessionColors map[string]string
	var err error
	if readOnly {
		sessionColors, err = loadAgentSessionColorsBounded(ctx, agentPanesFromRows(rows), fallbackSessionColor, budget)
	} else {
		sessionColors, err = loadAgentSessionColors(agentPanesFromRows(rows), fallbackSessionColor)
	}
	if err != nil {
		return agentInventory{}, err
	}
	return agentInventory{rows: rows, sessionColors: sessionColors}, nil
}

func projectLiveAgentInventory(panes []tmux.Pane, snapshot processSnapshot, annotations map[string]agentstatus.Annotation) []agentRow {
	return agentRowsForPanes(validLiveAgentPanes(panes), snapshot, annotations)
}

func validLiveAgentPanes(panes []tmux.Pane) []tmux.Pane {
	validated := make([]tmux.Pane, 0, len(panes))
	for _, pane := range panes {
		if validLiveAgentPane(pane) {
			validated = append(validated, pane)
		}
	}
	return validated
}

func validLiveAgentPane(pane tmux.Pane) bool {
	if !tmux.IsCanonicalID(pane.SessionID, '$') || !tmux.IsCanonicalID(pane.WindowID, '@') || !tmux.IsCanonicalID(pane.PaneID, '%') || !canonicalPositiveDecimal(pane.PanePID) {
		return false
	}
	for _, field := range []string{
		pane.SessionName, pane.SessionPath, pane.WindowName, pane.WindowIndex, pane.PaneIndex,
		pane.PaneTitle, pane.CurrentCommand, pane.CurrentPath,
	} {
		if strings.ContainsAny(field, "\x00\x1f\r\n") {
			return false
		}
	}
	return true
}

func canonicalPositiveDecimal(value string) bool {
	if !canonicalUnsignedDecimal(value) {
		return false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && parsed > 0
}

func canonicalUnsignedDecimal(value string) bool {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func loadAgentHookAnnotations(ctx context.Context, panes []tmux.Pane, snapshot processSnapshot, now time.Time) map[string]agentstatus.Annotation {
	return loadAgentHookAnnotationsMode(ctx, panes, snapshot, now, false, nil)
}

func loadAgentHookAnnotationsMode(ctx context.Context, panes []tmux.Pane, snapshot processSnapshot, now time.Time, readOnly bool, budget *tmux.OutputBudget) map[string]agentstatus.Annotation {
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return nil
	}
	store, err := openDefaultAgentStatusStore()
	if err != nil {
		return nil
	}
	serverID := agentstatus.ServerFingerprint(os.Getenv("TMUX"))
	live := make([]agentstatus.LivePane, 0, len(panes))
	for _, pane := range panes {
		provider := agentProviderForName(agentPaneName(pane, snapshot))
		identity := agentPaneIdentity(serverID, pane, snapshot, provider)
		if snapshot.available && provider != "" && identity.ProviderPID == 0 {
			// A current provider identity without a matching process is not a
			// live hook claim. Keep the provider restriction so reconciliation
			// retires a hard-killed process incarnation.
			identity.ProviderPID = -1
		}
		live = append(live, agentstatus.LivePane{
			Pane:     identity,
			Provider: provider,
		})
	}
	var annotations []agentstatus.Annotation
	if readOnly {
		var consume func(int) bool
		if budget != nil {
			consume = budget.Consume
		}
		annotations, _ = store.SnapshotReadOnly(ctx, live, now, consume)
	} else {
		annotations, _ = store.Snapshot(ctx, live, now)
	}
	byPane := make(map[string]agentstatus.Annotation, len(annotations))
	for _, annotation := range annotations {
		if annotation.Pane.PaneID != "" {
			byPane[annotation.Pane.PaneID] = annotation
		}
	}
	return byPane
}

func agentPaneIdentity(serverID string, pane tmux.Pane, snapshot processSnapshot, provider agentstatus.Provider) agentstatus.PaneIdentity {
	pid, _ := strconv.Atoi(pane.PanePID)
	return agentstatus.PaneIdentity{
		ServerID:      serverID,
		PaneID:        pane.PaneID,
		PanePID:       pid,
		ProviderPID:   snapshot.providerPIDs[pid][provider],
		TmuxSessionID: pane.SessionID,
	}
}

func agentRowsForPanes(panes []tmux.Pane, snapshot processSnapshot, annotations map[string]agentstatus.Annotation) []agentRow {
	rows := make([]agentRow, 0, len(panes))
	for _, pane := range panes {
		name := agentPaneName(pane, snapshot)
		detected := isDirectAgentPane(pane) || isProcessTreeAgentPane(pane, snapshot)
		annotation, claimed := annotations[pane.PaneID]
		claimed = claimed && agentAnnotationMatchesPane(annotation, pane)
		detectedProvider := agentProviderForName(name)
		panePID, _ := strconv.Atoi(pane.PanePID)
		if claimed && snapshot.available && annotation.Pane.ProviderPID > 0 && snapshot.providerPIDs[panePID][annotation.Provider] != annotation.Pane.ProviderPID {
			claimed = false
		}
		if claimed && detectedProvider != "" && detectedProvider != annotation.Provider {
			claimed = false
		}
		if !detected && !claimed {
			continue
		}
		provider := agentProviderForName(name)
		if claimed {
			provider = annotation.Provider
			name = string(provider)
		}
		status := agentPaneStatusForProvider(pane, snapshot, provider)
		source, rawEvent := agentFallbackEvidence(pane, snapshot, provider)
		providerPID := snapshot.providerPIDs[panePID][provider]
		if claimed && annotation.Pane.ProviderPID > 0 {
			providerPID = annotation.Pane.ProviderPID
		}
		row := agentRow{pane: pane, provider: provider, providerPID: providerPID, status: status, name: name, statusSource: source, rawEvent: rawEvent, fresh: true}
		if claimed {
			if hookStatus, authoritative := agentStatusFromAnnotation(annotation, pane, snapshot); authoritative {
				row.status = hookStatus
				row.providerSession = annotation.ProviderSessionID
				row.statusSource = annotation.Source
				row.rawEvent = annotation.RawEvent
				row.reason = annotation.Reason
				row.updatedAt = annotation.UpdatedAt
				row.turnStartedAt = annotation.TurnStartedAt
				row.stateChangedAt = annotation.StateChangedAt
				row.lastEventAt = annotation.LastEventAt
				row.fresh = annotation.Fresh
				if hookStatus == agentStatusCompleted {
					row.acknowledgeToken = annotation.AcknowledgeToken
				}
			}
			for _, child := range annotation.Children {
				if child.Fresh {
					row.children = append(row.children, child)
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func agentRowsForPicker(rows []agentRow) []agentRow {
	ordered := append([]agentRow(nil), rows...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := agentStatusRank(ordered[i].status), agentStatusRank(ordered[j].status)
		if left != right {
			return left < right
		}
		if ordered[i].updatedAt.Equal(ordered[j].updatedAt) {
			return false
		}
		return ordered[i].updatedAt.After(ordered[j].updatedAt)
	})
	return ordered
}

func agentRowsForSnapshot(rows []agentRow) []agentRow {
	return agentRowsForPicker(rows)
}

func agentFallbackEvidence(pane tmux.Pane, snapshot processSnapshot, provider agentstatus.Provider) (string, string) {
	switch provider {
	case agentstatus.ProviderCodex:
		if _, ok := codexPaneTitleStatus(pane); ok {
			return "terminal-title", "codex-title"
		}
	case agentstatus.ProviderClaude:
		if _, ok := claudeStatusFromPaneTitle(pane.PaneTitle); ok {
			return "terminal-title", "claude-title"
		}
	}
	if processAgentPaneStatus(pane, snapshot) != agentStatusUnknown {
		return "process", "process-state"
	}
	return "fallback", "unknown"
}

func agentPreviewRows(rows []agentRow, now time.Time) map[string]agentPreviewData {
	preview := make(map[string]agentPreviewData, len(rows))
	for _, row := range rows {
		children := make([]string, 0, len(row.children))
		for _, child := range row.children {
			label := strings.TrimSpace(strings.Join([]string{child.ID, child.Type, string(child.State), child.RawEvent}, " | "))
			children = append(children, label)
		}
		age := ""
		if !row.updatedAt.IsZero() {
			age = compactAgentAge(now.Sub(row.updatedAt))
		}
		preview[row.pane.PaneID] = agentPreviewData{
			State:           string(row.status),
			Provider:        nonEmpty(string(row.provider), row.name),
			ProviderSession: row.providerSession,
			Source:          row.statusSource,
			Event:           row.rawEvent,
			Reason:          row.reason,
			Age:             age,
			Fresh:           row.fresh,
			Children:        children,
		}
	}
	return preview
}

func compactAgentAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Second:
		return "now"
	case age < time.Minute:
		return fmt.Sprintf("%ds ago", int(age/time.Second))
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	default:
		return fmt.Sprintf("%dh ago", int(age/time.Hour))
	}
}

func agentAnnotationMatchesPane(annotation agentstatus.Annotation, pane tmux.Pane) bool {
	if !annotation.Fresh || annotation.Pane.PaneID != pane.PaneID {
		return false
	}
	if annotation.Pane.TmuxSessionID != "" && pane.SessionID != "" && annotation.Pane.TmuxSessionID != pane.SessionID {
		return false
	}
	serverID := agentstatus.ServerFingerprint(os.Getenv("TMUX"))
	if annotation.Pane.ServerID != "" && serverID != "" && annotation.Pane.ServerID != serverID {
		return false
	}
	if pane.PanePID != "" && annotation.Pane.PanePID != 0 && strconv.Itoa(annotation.Pane.PanePID) != pane.PanePID {
		return false
	}
	return true
}

func agentStatusFromAnnotation(annotation agentstatus.Annotation, pane tmux.Pane, snapshot processSnapshot) (agentStatus, bool) {
	if !annotation.Fresh {
		return "", false
	}
	status := agentStatus(annotation.State)
	switch status {
	case agentStatusAttention, agentStatusWorking, agentStatusCompleted, agentStatusWaiting:
	default:
		return "", false
	}
	if annotation.Provider == agentstatus.ProviderCodex && status == agentStatusAttention && strings.EqualFold(annotation.RawEvent, "PermissionRequest") {
		fallback := agentPaneStatusForProvider(pane, snapshot, annotation.Provider)
		if fallback != agentStatusAttention {
			return "", false
		}
	}
	return status, true
}

func agentProviderForName(name string) agentstatus.Provider {
	switch name {
	case "codex":
		return agentstatus.ProviderCodex
	case "claude":
		return agentstatus.ProviderClaude
	default:
		return ""
	}
}

func agentPanesFromRows(rows []agentRow) []tmux.Pane {
	panes := make([]tmux.Pane, 0, len(rows))
	for _, row := range rows {
		panes = append(panes, row.pane)
	}
	return panes
}

func agentItemsForRows(rows []agentRow, currentPaneID string, sessionColors map[string]string, agentsConfig config.AgentsConfig, list bool) []picker.Item[menuItem] {
	items := make([]picker.Item[menuItem], 0, len(rows))
	for _, row := range rows {
		label := agentPaneLabelWithConfig(row.pane, currentPaneID, row.status, row.name, sessionColorForPane(row.pane, sessionColors), agentsConfig)
		if list {
			label = agentListPaneLabelWithConfig(row.pane, currentPaneID, row.status, row.name, sessionColorForPane(row.pane, sessionColors), agentsConfig)
		}
		if count := len(row.children); count > 0 {
			label += "  " + dim(fmt.Sprintf("+%d", count))
		}
		switchDispatch := action.SwitchPane(row.pane)
		switchDispatch.ProviderPID = row.providerPID
		items = append(items, picker.Item[menuItem]{
			Label:   label,
			Preview: row.pane.PaneID,
			Value: menuItem{
				dispatch:      switchDispatch,
				agentPaneID:   row.pane.PaneID,
				agentAckToken: row.acknowledgeToken,
			},
		})
	}
	return items
}

func agentInitialIndex(items []picker.Item[menuItem], paneID string) int {
	if paneID == "" {
		return 0
	}
	for index, item := range items {
		if item.Value.agentPaneID == paneID {
			return index
		}
	}
	return 0
}

func agentStatusRank(status agentStatus) int {
	switch status {
	case agentStatusAttention:
		return 0
	case agentStatusWorking:
		return 1
	case agentStatusCompleted:
		return 2
	case agentStatusWaiting:
		return 3
	default:
		return 4
	}
}

func acknowledgeAgentCompletion(ctx context.Context, token string) error {
	store, err := openDefaultAgentStatusStore()
	if err != nil {
		return err
	}
	_, err = store.Acknowledge(ctx, token, time.Now())
	return err
}

func agentItems(panes []tmux.Pane, currentPaneID string) []picker.Item[menuItem] {
	return agentItemsWithSessionColors(panes, currentPaneID, nil)
}

func agentItemsWithSessionColors(panes []tmux.Pane, currentPaneID string, sessionColors map[string]string) []picker.Item[menuItem] {
	snapshot := loadAgentProcessSnapshot(panes)
	return agentItemsWithProcessSnapshotAndSessionColors(panes, currentPaneID, snapshot, sessionColors)
}

func agentItemsWithProcessSnapshot(panes []tmux.Pane, currentPaneID string, snapshot processSnapshot) []picker.Item[menuItem] {
	return agentItemsWithProcessSnapshotAndSessionColors(panes, currentPaneID, snapshot, nil)
}

func agentItemsWithProcessSnapshotAndSessionColors(panes []tmux.Pane, currentPaneID string, snapshot processSnapshot, sessionColors map[string]string) []picker.Item[menuItem] {
	return agentItemsWithProcessSnapshotAndSessionColorsAndConfig(panes, currentPaneID, snapshot, sessionColors, config.Default().Agents)
}

func agentItemsWithProcessSnapshotAndSessionColorsAndConfig(panes []tmux.Pane, currentPaneID string, snapshot processSnapshot, sessionColors map[string]string, agentsConfig config.AgentsConfig) []picker.Item[menuItem] {
	agents := agentPanesWithProcessSnapshot(panes, snapshot)
	items := make([]picker.Item[menuItem], 0, len(agents))
	for _, p := range agents {
		items = append(items, picker.Item[menuItem]{
			Label:   agentPaneLabelWithConfig(p, currentPaneID, agentPaneStatus(p, snapshot), agentPaneName(p, snapshot), sessionColorForPane(p, sessionColors), agentsConfig),
			Preview: p.PaneID,
			Value:   menuItem{dispatch: action.SwitchPane(p)},
		})
	}
	return items
}

func agentPreviewWindow(terminalRows int) string {
	size := "60%"
	if previewRows := terminalRows - agentVisibleResultRows - agentPickerNonResultRows; previewRows > 0 {
		size = strconv.Itoa(previewRows)
	}
	return strings.Join([]string{"down", size, "border-rounded", "wrap", "follow"}, ":")
}

func agentTerminalRows() int {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return parseTerminalRows(string(out))
}

func parseTerminalRows(output string) int {
	fields := strings.Fields(output)
	if len(fields) != 2 {
		return 0
	}
	rows, err := strconv.Atoi(fields[0])
	if err != nil || rows <= 0 {
		return 0
	}
	return rows
}

func loadAgentSessionColors(panes []tmux.Pane, fallback string) (map[string]string, error) {
	colors := make(map[string]string)
	for _, p := range panes {
		key := paneSessionKey(p)
		if _, ok := colors[key]; ok {
			continue
		}
		color := fallback
		if p.SessionPath != "" {
			cfg, err := config.LoadForContext(p.SessionPath, p.SessionPath)
			if err != nil {
				return nil, fmt.Errorf("load config for session %q: %w", p.SessionName, err)
			}
			color = cfg.Session.Color
		}
		colors[key] = color
	}
	return colors, nil
}

func loadAgentSessionColorsBounded(ctx context.Context, panes []tmux.Pane, fallback string, budget *tmux.OutputBudget) (map[string]string, error) {
	colors := make(map[string]string)
	var consume func(int) bool
	if budget != nil {
		consume = budget.Consume
	}
	for _, p := range panes {
		key := paneSessionKey(p)
		if _, ok := colors[key]; ok {
			continue
		}
		color := fallback
		if p.SessionPath != "" {
			cfg, err := config.LoadForContextBounded(ctx, p.SessionPath, p.SessionPath, consume)
			if err != nil {
				return nil, fmt.Errorf("load config for session %q: %w", p.SessionName, err)
			}
			color = cfg.Session.Color
		}
		colors[key] = color
	}
	return colors, nil
}

func sessionColorForPane(p tmux.Pane, colors map[string]string) string {
	if color := colors[paneSessionKey(p)]; color != "" {
		return color
	}
	return config.DefaultSessionColor
}

func paneSessionKey(p tmux.Pane) string {
	if p.SessionID != "" {
		return p.SessionID
	}
	return p.SessionName
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
	return loadAgentProcessSnapshotBounded(context.Background(), panes, tmux.NewOutputBudget(agentProcessOutputBytes))
}

func loadAgentProcessSnapshotBounded(ctx context.Context, panes []tmux.Pane, budget *tmux.OutputBudget) processSnapshot {
	for _, p := range panes {
		if p.PanePID != "" {
			return agentProcessSnapshotBounded(ctx, budget, agentProcessOutputBytes)
		}
	}
	return processSnapshot{}
}

func agentPaneStatus(p tmux.Pane, snapshot processSnapshot) agentStatus {
	return agentPaneStatusForProvider(p, snapshot, agentProviderForName(agentPaneName(p, snapshot)))
}

func agentPaneStatusForProvider(p tmux.Pane, snapshot processSnapshot, provider agentstatus.Provider) agentStatus {
	switch provider {
	case agentstatus.ProviderCodex:
		if status, ok := codexPaneTitleStatus(p); ok {
			return status
		}
	case agentstatus.ProviderClaude:
		if status, ok := claudeStatusFromPaneTitle(p.PaneTitle); ok {
			return status
		}
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
		if status, ok := codexRunStateStatus(parts[0]); ok {
			return codexPaneTitleInfo{
				threadTitle: strings.TrimSpace(strings.Join(parts[1:], "|")),
				runState:    codexRunStateFromStatus(status),
			}, true
		}
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
	roots        map[int]bool
	statuses     map[int]agentStatus
	names        map[int]string
	providerPIDs map[int]map[agentstatus.Provider]int
	available    bool
}

func agentProcessSnapshotBounded(ctx context.Context, budget *tmux.OutputBudget, maxBytes int64) processSnapshot {
	out, err := tmux.RunCommandBounded(ctx, budget, maxBytes, "ps", "-axo", "pid=,ppid=,state=,command=")
	if err != nil {
		return processSnapshot{}
	}
	processes, ok := parseProcessListBounded(out, agentProcessRows, agentProcessFieldBytes)
	if !ok {
		return processSnapshot{}
	}
	snapshot := buildProcessSnapshot(processes)
	snapshot.available = true
	return snapshot
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
	providerPIDs := make(map[int]map[agentstatus.Provider]int)
	visiting := make(map[int]bool)
	scanned := make(map[int]bool)
	var scan func(int) (bool, bool, string)
	scan = func(pid int) (bool, bool, string) {
		if scanned[pid] {
			return roots[pid], statuses[pid] == agentStatusWorking, names[pid]
		}
		if name := agents[pid]; name != "" {
			roots[pid] = true
			scanned[pid] = true
			names[pid] = name
			provider := agentProviderForName(name)
			if provider != "" {
				providerPIDs[pid] = map[agentstatus.Provider]int{provider: pid}
			}
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
				for provider, providerPID := range providerPIDs[child] {
					if providerPIDs[pid] == nil {
						providerPIDs[pid] = make(map[agentstatus.Provider]int)
					}
					if providerPIDs[pid][provider] == 0 || providerPID < providerPIDs[pid][provider] {
						providerPIDs[pid][provider] = providerPID
					}
				}
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
		scanned[pid] = true
		return found, working, name
	}
	for _, p := range processes {
		scan(p.pid)
	}
	return processSnapshot{roots: roots, statuses: statuses, names: names, providerPIDs: providerPIDs, available: true}
}

func processLooksRunning(p processInfo) bool {
	return strings.HasPrefix(p.state, "R")
}

func parseProcessList(out string) []processInfo {
	processes, _ := parseProcessListBounded(out, agentProcessRows, agentProcessFieldBytes)
	return processes
}

func parseProcessListBounded(out string, maxRows, maxFieldBytes int) ([]processInfo, bool) {
	if maxRows <= 0 || maxFieldBytes <= 0 {
		return nil, false
	}
	processes := make([]processInfo, 0)
	rows := 0
	for offset := 0; offset < len(out); {
		end := strings.IndexByte(out[offset:], '\n')
		if end < 0 {
			end = len(out)
		} else {
			end += offset
		}
		line := out[offset:end]
		rows++
		if rows > maxRows || len(line) > maxFieldBytes {
			return nil, false
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			offset = nextProcessLineOffset(end, len(out))
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			offset = nextProcessLineOffset(end, len(out))
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			offset = nextProcessLineOffset(end, len(out))
			continue
		}
		processes = append(processes, processInfo{
			pid:     pid,
			ppid:    ppid,
			state:   fields[2],
			command: strings.Join(fields[3:], " "),
		})
		offset = nextProcessLineOffset(end, len(out))
	}
	return processes, true
}

func nextProcessLineOffset(end, length int) int {
	if end < length {
		return end + 1
	}
	return end
}
