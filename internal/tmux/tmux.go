package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const sep = "\x1f"

type Pane struct {
	SessionName     string
	SessionID       string
	SessionPath     string
	WindowName      string
	WindowID        string
	WindowIndex     string
	PaneIndex       string
	PaneID          string
	PanePID         string
	PaneTitle       string
	CurrentCommand  string
	CurrentPath     string
	PaneActive      bool
	WindowActive    bool
	AutomaticRename bool
}

func Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func Exec(ctx context.Context, args ...string) error {
	_, err := Run(ctx, args...)
	return err
}

func Display(ctx context.Context, format string) (string, error) {
	return Run(ctx, "display-message", "-p", format)
}

func ListPanes(ctx context.Context) ([]Pane, error) {
	format := strings.Join([]string{
		"#{session_name}",
		"#{session_id}",
		"#{window_name}",
		"#{window_id}",
		"#{window_index}",
		"#{pane_index}",
		"#{pane_id}",
		"#{pane_title}",
		"#{pane_current_command}",
		"#{pane_current_path}",
		"#{pane_active}",
		"#{window_active}",
		"#{pane_pid}",
		"#{automatic-rename}",
		"#{session_path}",
	}, sep)
	out, err := Run(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		return nil, err
	}
	return ParsePanes(out), nil
}

func ParsePanes(out string) []Pane {
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, sep)
		if len(parts) < 12 {
			continue
		}
		panePID := ""
		if len(parts) > 12 {
			panePID = parts[12]
		}
		automaticRename := false
		if len(parts) > 13 {
			automaticRename = parts[13] == "1"
		}
		sessionPath := ""
		if len(parts) > 14 {
			sessionPath = parts[14]
		}
		panes = append(panes, Pane{
			SessionName:     parts[0],
			SessionID:       parts[1],
			SessionPath:     sessionPath,
			WindowName:      parts[2],
			WindowID:        parts[3],
			WindowIndex:     parts[4],
			PaneIndex:       parts[5],
			PaneID:          parts[6],
			PanePID:         panePID,
			PaneTitle:       parts[7],
			CurrentCommand:  parts[8],
			CurrentPath:     parts[9],
			PaneActive:      parts[10] == "1",
			WindowActive:    parts[11] == "1",
			AutomaticRename: automaticRename,
		})
	}
	return panes
}

func CapturePane(ctx context.Context, paneID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 100
	}
	start := "-" + strconv.Itoa(lines)
	return Run(ctx, "capture-pane", "-p", "-S", start, "-t", paneID)
}

func SwitchPane(ctx context.Context, p Pane) error {
	return Exec(ctx,
		"switch-client", "-t", p.SessionID,
		";", "select-window", "-t", p.WindowID,
		";", "select-pane", "-t", p.PaneID,
	)
}
