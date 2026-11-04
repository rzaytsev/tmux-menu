package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tmux-menu/internal/displaytext"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
	"tmux-menu/internal/tmux"
)

type agentPickerMetadata struct {
	AckToken string `json:"ack,omitempty"`
	Workdir  string `json:"workdir,omitempty"`
}

// Prepare slow inventory work outside fzf's tracking window. Only loading a
// completed local file tracks identity; query edits cancel this background job.
func runAgentPickerRefresh(ctx context.Context, path, columns, query string, out io.Writer) error {
	var rows bytes.Buffer
	if err := runAgentPickerRows(ctx, path, columns, query, &rows); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "live-*.tsv")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(rows.Bytes()); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ready := path + ".live.tsv"
	if err := os.Rename(file.Name(), ready); err != nil {
		return err
	}
	fmt.Fprintln(out, "track-current+reload-sync(cat "+shellquote.Quote(ready)+")+refresh-preview")
	return nil
}

func encodeAgentPickerMetadata(row agentRow) string {
	metadata := agentPickerMetadata{AckToken: row.acknowledgeToken, Workdir: row.pane.CurrentPath}
	if strings.ContainsFunc(metadata.Workdir, displaytext.UnsafeControl) {
		metadata.Workdir = ""
	}
	data, _ := json.Marshal(metadata)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeAgentPickerMetadata(value string, target *agentPickerMetadata) error {
	if len(value) > 128<<10 {
		return fmt.Errorf("agent selection metadata too large")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	if strings.ContainsFunc(target.Workdir+target.AckToken, displaytext.UnsafeControl) {
		return fmt.Errorf("agent selection metadata contains controls")
	}
	return nil
}

func agentStateAge(row agentRow, now time.Time) string {
	// Hook transition timestamps survive progress events. OSC UpdatedAt and
	// terminal/process observations are event times, not proven transitions.
	if row.statusSource != "hook" || row.stateChangedAt.IsZero() || row.stateChangedAt.After(now) {
		return "—"
	}
	return strings.TrimSuffix(compactAgentAge(now.Sub(row.stateChangedAt)), " ago")
}

func agentAttentionReason(row agentRow) string {
	if row.status != agentStatusAttention {
		return "—"
	}
	if row.reason != "" {
		return row.reason
	}
	return "input required"
}

func agentSearchText(row agentRow) string {
	return displaytext.Line(strings.Join([]string{string(row.status), row.name, row.pane.SessionName,
		agentListPaneTitle(row.pane, row.name), row.pane.CurrentPath, row.reason}, " "))
}

func agentSummary(rows []agentRow, snapshot agentPreviewSnapshot, shown int) string {
	counts := map[agentStatus]int{}
	for _, row := range rows {
		counts[row.status]++
	}
	filter := nonEmpty(snapshot.Filter, "all")
	if snapshot.SessionOnly {
		filter += ", current session"
	}
	return fmt.Sprintf("%d shown / %d live · %d attention · %d working · %d completed · %d waiting · %d unknown  [%s]  Ctrl-A attention · Ctrl-S session · Ctrl-D completed · Ctrl-Y copy dir",
		shown, len(rows), counts[agentStatusAttention], counts[agentStatusWorking], counts[agentStatusCompleted], counts[agentStatusWaiting], counts[agentStatusUnknown], filter)
}

// Frozen ordering and exact identity are preserved until a manual refresh.
func frozenAgentRows(snapshot agentPreviewSnapshot, rows []agentRow) []agentRow {
	byPane := make(map[string]agentRow, len(rows))
	for _, row := range rows {
		byPane[row.pane.PaneID] = row
	}
	ordered := make([]agentRow, 0, len(snapshot.Order))
	for _, id := range snapshot.Order {
		row, ok := byPane[id]
		identity := snapshot.Rows[id]
		if !ok || row.pane.SessionID != identity.SessionID || row.pane.WindowID != identity.WindowID || row.pane.PanePID != identity.PanePID ||
			snapshot.ProviderPIDs[id] != row.providerPID || identity.Provider != nonEmpty(string(row.provider), row.name) {
			// Keep an empty slot so wire IDs still address the original items.
			ordered = append(ordered, agentRow{})
			continue
		}
		ordered = append(ordered, row)
	}
	return ordered
}

func runAgentPickerRows(ctx context.Context, path, columnsText, query string, out io.Writer) (resultErr error) {
	defer func() {
		if resultErr != nil {
			fmt.Fprintln(out, "\t\t"+displaytext.Line("Agent inventory unavailable · Ctrl-R to retry"))
			fmt.Fprintln(out, "\t\t"+displaytext.Line("STATUS  AGENT  SESSION  AGE  THREAD  WORKDIR  REASON"))
			fmt.Fprintln(out, "-1\t\t"+displaytext.Line(resultErr.Error()))
			resultErr = nil
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	snapshot, err := readAgentPreviewSnapshot(path)
	if err != nil {
		return err
	}
	columns, err := strconv.Atoi(columnsText)
	if err != nil || columns <= 0 {
		columns = 120
	}
	budget := tmux.NewOutputBudget(agentInventoryOutputBytes)
	panes, err := tmux.ListPanesBounded(ctx, budget, tmux.DefaultPaneListLimits())
	if err != nil {
		return err
	}
	// Polling uses the shared projection with read-only hook lookups.
	inventory, err := agentInventoryForPanesBoundedMode(ctx, panes, "auto", time.Now(), budget, true)
	if err != nil {
		return err
	}
	return writeAgentPickerRows(ctx, snapshot, frozenAgentRows(snapshot, inventory.rows), inventory.sessionColors, columns, query, out)
}

func writeAgentPickerRows(ctx context.Context, snapshot agentPreviewSnapshot, rows []agentRow, colors map[string]string, columns int, query string, out io.Writer) error {
	items, heading := agentPickerItemsForRows(rows, snapshot.OriginPane, colors, snapshot.Agents, columns)
	var search strings.Builder
	live := make([]agentRow, 0, len(rows))
	for i, row := range rows {
		if row.pane.PaneID == "" {
			continue
		}
		live = append(live, row)
		if snapshot.Filter != "" && string(row.status) != snapshot.Filter || snapshot.SessionOnly && row.pane.SessionID != snapshot.OriginSession {
			continue
		}
		fmt.Fprintf(&search, "%d\t%s\n", i, agentSearchText(row))
	}
	matched, err := picker.Filter(ctx, search.String(), query, "2..", true)
	if err != nil {
		return err
	}
	indices := []int{}
	for _, line := range strings.Split(strings.TrimSuffix(matched, "\n"), "\n") {
		id, _, found := strings.Cut(line, "\t")
		if index, err := strconv.Atoi(id); found && err == nil && index >= 0 && index < len(items) {
			indices = append(indices, index)
		}
	}
	fmt.Fprintln(out, "\t\t"+displaytext.Line(agentSummary(live, snapshot, len(indices))))
	fmt.Fprintln(out, "\t\t"+displaytext.Line(heading))
	if len(indices) == 0 {
		fmt.Fprintln(out, "-1\t\tNo matching agents")
	}
	for _, i := range indices {
		// IDs address frozen original items, independent of filtering or status.
		fmt.Fprintf(out, "%d\t%s\t%s\t%s\n", i, items[i].Preview, displaytext.Line(items[i].Label), encodeAgentPickerMetadata(rows[i]))
	}
	return nil
}
