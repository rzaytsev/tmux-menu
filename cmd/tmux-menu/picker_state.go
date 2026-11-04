package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"tmux-menu/internal/fileutil"
	"tmux-menu/internal/picker"
)

type pickerLoopState struct {
	Views       map[string]*picker.State `json:"views"`
	AgentFilter string                   `json:"agent_filter,omitempty"`
	SessionOnly bool                     `json:"session_only,omitempty"`
}

type pickerLoopStateKey struct{}

func loadPickerLoopState() (*pickerLoopState, error) {
	state := &pickerLoopState{Views: map[string]*picker.State{}}
	if path := os.Getenv("TMUX_MENU_PICKER_STATE_FILE"); path != "" {
		data, err := fileutil.Read(path, 1<<20)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, state); err != nil {
				return nil, err
			}
		}
	}
	if state.Views == nil {
		state.Views = map[string]*picker.State{}
	}
	return state, nil
}

func savePickerLoopState(state *pickerLoopState) error {
	if path := os.Getenv("TMUX_MENU_PICKER_STATE_FILE"); path != "" {
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if len(data) > 1<<20 {
			return fmt.Errorf("picker state exceeds size limit")
		}
		return os.WriteFile(path, data, 0600)
	}
	return nil
}

func agentFilterFromContext(ctx context.Context) (string, bool) {
	if state, ok := ctx.Value(pickerLoopStateKey{}).(*pickerLoopState); ok {
		return state.AgentFilter, state.SessionOnly
	}
	return "", false
}
