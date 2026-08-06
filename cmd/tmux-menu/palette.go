package main

import (
	"context"

	"tmux-menu/internal/action"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

func selectPalette(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	var agentRows []picker.Item[menuItem]
	for _, section := range cfg.Palette.Sections {
		if section != "agents" {
			continue
		}
		snapshot := loadAgentProcessSnapshot(panes)
		agents := agentPanesWithProcessSnapshot(panes, snapshot)
		sessionColors, err := loadAgentSessionColors(agents, cfg.Session.Color)
		if err != nil {
			return picker.Result[menuItem]{}, err
		}
		agentRows = agentItemsWithProcessSnapshotAndSessionColorsAndConfig(panes, rt.OriginPane, snapshot, sessionColors, cfg.Agents)
		break
	}
	items := paletteItemsWithAgentSource(panes, rt.SessionID, rt.OriginPane, cfg.Palette.Sections, func() []picker.Item[menuItem] {
		return agentRows
	})
	return picker.SelectWithExpect(ctx, "tmux-menu> ", items, viewSwitchKeys, viewSwitchFooter())
}

func paletteItems(panes []tmux.Pane, currentSessionID string, currentPaneID string, sections []string) []picker.Item[menuItem] {
	return paletteItemsWithAgentSource(panes, currentSessionID, currentPaneID, sections, func() []picker.Item[menuItem] {
		return agentItems(panes, currentPaneID)
	})
}

func paletteItemsWithAgentSource(panes []tmux.Pane, currentSessionID string, currentPaneID string, sections []string, agentSource func() []picker.Item[menuItem]) []picker.Item[menuItem] {
	items := make([]picker.Item[menuItem], 0, len(panes)+len(uniqueSessions(panes)))
	for _, section := range sections {
		switch section {
		case "agents":
			if agentSource != nil {
				items = append(items, agentSource()...)
			}
		case "sessions":
			items = append(items, sessionItems(panes, currentSessionID)...)
		case "panes":
			items = append(items, paneItems(panes, currentPaneID)...)
		}
	}
	return items
}

func sessionItems(panes []tmux.Pane, currentSessionID string) []picker.Item[menuItem] {
	items := make([]picker.Item[menuItem], 0, len(uniqueSessions(panes)))
	for _, session := range uniqueSessions(panes) {
		items = append(items, picker.Item[menuItem]{
			Label: sessionLabel(session, panes, currentSessionID),
			Value: menuItem{dispatch: action.SwitchSession(session)},
		})
	}
	return items
}

func paneItems(panes []tmux.Pane, currentPaneID string) []picker.Item[menuItem] {
	items := make([]picker.Item[menuItem], 0, len(panes))
	for _, p := range panes {
		label := paneLabel(p, currentPaneID)
		items = append(items, picker.Item[menuItem]{
			Label: label,
			Value: menuItem{dispatch: action.SwitchPane(p)},
		})
	}
	return items
}
func uniqueSessions(panes []tmux.Pane) []tmux.Pane {
	seen := make(map[string]bool)
	sessions := make([]tmux.Pane, 0)
	for _, p := range panes {
		if seen[p.SessionID] {
			continue
		}
		seen[p.SessionID] = true
		sessions = append(sessions, p)
	}
	return sessions
}
