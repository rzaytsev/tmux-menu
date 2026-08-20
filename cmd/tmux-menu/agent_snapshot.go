package main

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"tmux-menu/internal/config"
)

const agentStatusSnapshotVersion = 1

type agentStatusSnapshot struct {
	Version int                      `json:"version"`
	Agents  []agentStatusSnapshotRow `json:"agents"`
}

type agentStatusSnapshotRow struct {
	PaneID   string `json:"pane_id"`
	Provider string `json:"provider"`
	Status   string `json:"status"`
	Source   string `json:"source"`
	Fresh    bool   `json:"fresh"`
}

var writeAgentStatusSnapshotCommand = writeAgentStatusSnapshot

func writeAgentStatusSnapshot(ctx context.Context, out io.Writer) error {
	inventory, err := loadAgentInventory(ctx, config.DefaultSessionColor, time.Now())
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
			PaneID:   row.pane.PaneID,
			Provider: provider,
			Status:   string(row.status),
			Source:   row.statusSource,
			Fresh:    row.fresh,
		})
	}
	return agentStatusSnapshot{Version: agentStatusSnapshotVersion, Agents: agents}
}
