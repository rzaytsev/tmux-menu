package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const sep = "\x1f"

var paneFormatFields = []string{
	"session_name",
	"session_id",
	"window_name",
	"window_id",
	"window_index",
	"pane_index",
	"pane_id",
	"pane_title",
	"pane_current_command",
	"pane_current_path",
	"pane_active",
	"window_active",
	"pane_pid",
	"automatic-rename",
	"session_path",
}

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
	out, err := runRaw(ctx, args...)
	return strings.TrimRight(out, "\n"), err
}

func runRaw(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func Exec(ctx context.Context, args ...string) error {
	_, err := Run(ctx, args...)
	return err
}

func Display(ctx context.Context, format string) (string, error) {
	return Run(ctx, "display-message", "-p", format)
}

func ListPanes(ctx context.Context) ([]Pane, error) {
	var format strings.Builder
	for _, field := range paneFormatFields {
		format.WriteString("#{n:")
		format.WriteString(field)
		format.WriteByte('}')
		format.WriteString(sep)
		format.WriteString("#{")
		format.WriteString(field)
		format.WriteByte('}')
	}
	out, err := runRaw(ctx, "list-panes", "-a", "-F", format.String())
	if err != nil {
		return nil, err
	}
	return ParsePanes(out), nil
}

func ParsePanes(out string) []Pane {
	var panes []Pane
	for offset := 0; offset < len(out); {
		parts, next, ok := parsePaneFields(out, offset)
		if !ok {
			break
		}
		offset = next
		panes = append(panes, Pane{
			SessionName:     parts[0],
			SessionID:       parts[1],
			SessionPath:     parts[14],
			WindowName:      parts[2],
			WindowID:        parts[3],
			WindowIndex:     parts[4],
			PaneIndex:       parts[5],
			PaneID:          parts[6],
			PanePID:         parts[12],
			PaneTitle:       parts[7],
			CurrentCommand:  parts[8],
			CurrentPath:     parts[9],
			PaneActive:      parts[10] == "1",
			WindowActive:    parts[11] == "1",
			AutomaticRename: parts[13] == "1",
		})
	}
	return panes
}

func parsePaneFields(out string, offset int) ([]string, int, bool) {
	parts := make([]string, len(paneFormatFields))
	for index := range parts {
		separator := strings.IndexByte(out[offset:], sep[0])
		if separator <= 0 {
			return nil, offset, false
		}
		separator += offset
		lengthText := out[offset:separator]
		for _, digit := range lengthText {
			if digit < '0' || digit > '9' {
				return nil, offset, false
			}
		}
		length, err := strconv.Atoi(lengthText)
		if err != nil || length < 0 {
			return nil, offset, false
		}
		fieldStart := separator + len(sep)
		fieldEnd := fieldStart + length
		if fieldEnd < fieldStart || fieldEnd > len(out) {
			return nil, offset, false
		}
		parts[index] = out[fieldStart:fieldEnd]
		offset = fieldEnd
	}
	if offset == len(out) {
		return parts, offset, true
	}
	if out[offset] != '\n' {
		return nil, offset, false
	}
	return parts, offset + 1, true
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
