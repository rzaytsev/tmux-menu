package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/config"
	"tmux-menu/internal/tmux"
)

const agentStatusSnapshotVersion = 1

type agentStatusSnapshot struct {
	Version int                      `json:"version"`
	Agents  []agentStatusSnapshotRow `json:"agents"`
}

type agentStatusSnapshotRow struct {
	ServerID          string `json:"server_id"`
	SessionID         string `json:"session_id"`
	WindowID          string `json:"window_id"`
	PanePID           string `json:"pane_pid"`
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	PaneID            string `json:"pane_id"`
	Provider          string `json:"provider"`
	Status            string `json:"status"`
	Source            string `json:"source"`
	Fresh             bool   `json:"fresh"`
}

var writeAgentStatusSnapshotCommand = writeAgentStatusSnapshot

func writeAgentStatusSnapshot(ctx context.Context, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	budget := tmux.NewOutputBudget(agentInventoryOutputBytes)
	panes, err := tmux.ListPanesBounded(ctx, budget, tmux.DefaultPaneListLimits())
	if err != nil {
		return err
	}
	inventory, err := agentInventoryForPanesBoundedMode(ctx, panes, config.DefaultSessionColor, time.Now(), budget, true)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(agentStatusSnapshotForRows(agentRowsForSnapshot(inventory.rows)))
}

func agentStatusSnapshotForRows(rows []agentRow) agentStatusSnapshot {
	agents := make([]agentStatusSnapshotRow, 0, len(rows))
	for _, row := range rows {
		provider := string(row.provider)
		if provider == "" {
			provider = row.name
		}
		agents = append(agents, agentStatusSnapshotRow{
			ServerID:          agentstatus.ServerFingerprint(os.Getenv("TMUX")),
			SessionID:         row.pane.SessionID,
			WindowID:          row.pane.WindowID,
			PanePID:           row.pane.PanePID,
			ProviderSessionID: row.providerSession,
			PaneID:            row.pane.PaneID,
			Provider:          provider,
			Status:            string(row.status),
			Source:            row.statusSource,
			Fresh:             row.fresh,
		})
	}
	return agentStatusSnapshot{Version: agentStatusSnapshotVersion, Agents: agents}
}
