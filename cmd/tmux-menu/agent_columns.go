package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"tmux-menu/internal/config"
	"tmux-menu/internal/displaytext"
	"tmux-menu/internal/picker"
)

func agentTerminalColumns() int {
	if columns, _, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		return columns
	}
	return 120
}

func agentPickerItemsForRows(rows []agentRow, currentPaneID string, sessionColors map[string]string, cfg config.AgentsConfig, columns int) ([]picker.Item[menuItem], string) {
	items := agentItemsForRows(rows, currentPaneID, sessionColors, cfg, false)
	headings := []string{"STATUS", "AGENT", "SESSION", "AGE", "THREAD", "WORKDIR", "REASON"}
	widths := []int{6, 5, 7, 3, 6, 7, 6}
	limits := []int{16, 18, 24, 8, 64, 40, 28}
	now := time.Now()
	cells := make([][]string, len(rows))
	for i, row := range rows {
		status := row.status
		if status == "" {
			status = agentStatusUnknown
		}
		title := agentListPaneTitle(row.pane, row.name)
		if row.pane.PaneID != "" && row.pane.PaneID == currentPaneID {
			title = cfg.Icons.Current + title
		}
		if len(row.children) > 0 {
			title += fmt.Sprintf(" +%d", len(row.children))
		}
		provider := colorAgentIcon(row.name, cfg)
		if row.name == "codex" || row.name == "claude" || cfg.Icons.Other != "" {
			provider = strings.TrimSuffix(provider, ansiReset) + " " + row.name + ansiReset
		}
		cells[i] = []string{
			strings.TrimSuffix(colorAgentStatusWithConfig(status, cfg), ansiReset) + " " + string(status) + ansiReset,
			provider,
			colorSessionWithColor(shortUUID(row.pane.SessionName), sessionColorForPane(row.pane, sessionColors)),
			dim(agentStateAge(row, now)),
			colorAgentThread(title, cfg),
			colorAgentText(agentListWorkdir(row.pane.CurrentPath), cfg.Colors.Workdir, false),
			colorAgentText(agentAttentionReason(row), cfg.Colors.Attention, false),
		}
		for j, cell := range cells[i] {
			cells[i][j] = displaytext.Line(cell)
			widths[j] = min(limits[j], max(widths[j], ansi.StringWidth(cells[i][j])))
		}
	}
	// Leave room for fzf's pointer, marker and scrollbar. Shrink the widest
	// descriptive column first, keeping status and provider readable.
	available := max(1, columns-4)
	for {
		total := 2 * (len(widths) - 1)
		widest := -1
		for j, width := range widths {
			total += width
			if j >= 2 && j != 3 && width > ansi.StringWidth(headings[j]) && (widest < 0 || width > widths[widest]) {
				widest = j
			}
		}
		if total <= available || widest < 0 {
			break
		}
		widths[widest]--
	}
	format := func(values []string) string {
		parts := make([]string, len(values))
		for j, value := range values {
			value = ansi.Truncate(value, widths[j], "…") + ansiReset
			parts[j] = value + strings.Repeat(" ", max(0, widths[j]-ansi.StringWidth(value)))
		}
		return ansi.Truncate(strings.TrimRight(strings.Join(parts, "  "), " "), available, "…") + ansiReset
	}
	for i := range items {
		items[i].Label = format(cells[i])
		items[i].Metadata = encodeAgentPickerMetadata(rows[i])
	}
	for i := range headings {
		headings[i] = dim(headings[i])
	}
	return items, format(headings)
}
