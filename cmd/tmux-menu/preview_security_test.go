package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"tmux-menu/internal/action"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/tmux"
)

func TestAgentPreviewRejectsReplacedPaneBeforeAndAfterCapture(t *testing.T) {
	for _, changedAt := range []int{1, 2} {
		t.Run(string(rune('0'+changedAt)), func(t *testing.T) {
			oldList, oldCapture := agentPreviewListPanes, agentHookTmuxOutput
			t.Cleanup(func() { agentPreviewListPanes, agentHookTmuxOutput = oldList, oldCapture })
			calls, captures := 0, 0
			agentPreviewListPanes = func(context.Context) ([]tmux.Pane, error) {
				calls++
				pid := "400"
				if calls >= changedAt {
					pid = "401"
				}
				return []tmux.Pane{{SessionID: "$1", WindowID: "@2", PaneID: "%4", PanePID: pid}}, nil
			}
			agentHookTmuxOutput = func(context.Context, ...string) ([]byte, error) {
				captures++
				return []byte("PRIVATE REPLACEMENT OUTPUT"), nil
			}
			path := filepath.Join(t.TempDir(), "snapshot.json")
			if err := writeAgentPreviewSnapshotFile(path, agentPreviewSnapshot{Version: 1, Rows: map[string]agentPreviewData{"%4": {SessionID: "$1", WindowID: "@2", PanePID: "400"}}}); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := runAgentPreview(context.Background(), path, "%4", &out); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "PRIVATE") || !strings.Contains(out.String(), "pane changed") || captures != changedAt-1 {
				t.Fatalf("stale preview: captures %d, output %q", captures, out.String())
			}
		})
	}
}

func TestPickerRefreshDoesNotDispatchSelection(t *testing.T) {
	old := selectModeForLoop
	t.Cleanup(func() { selectModeForLoop = old })
	calls := 0
	selectModeForLoop = func(_ context.Context, mode, _ string) (picker.Result[menuItem], error) {
		calls++
		if mode != "docs" {
			t.Fatalf("refresh changed mode to %q", mode)
		}
		if calls == 1 {
			return picker.Result[menuItem]{Key: "ctrl-r", Selected: true, Value: menuItem{dispatch: action.Dispatch{Mode: "invalid"}}}, nil
		}
		return picker.Result[menuItem]{}, picker.ErrCanceled
	}
	if err := runPickerLoop(context.Background(), "docs"); err != nil || calls != 2 {
		t.Fatalf("refresh: %v, calls %d", err, calls)
	}
}
