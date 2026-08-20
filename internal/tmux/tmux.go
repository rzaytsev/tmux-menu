package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const sep = "\x1f"

const (
	defaultCommandOutputBytes int64 = 4 << 20
	defaultPaneRows                 = 4096
	defaultPaneFieldBytes           = 64 << 10
)

var ErrOutputLimit = errors.New("command output limit exceeded")

type OutputBudget struct {
	limit int64
	used  atomic.Int64
}

func NewOutputBudget(limit int64) *OutputBudget {
	if limit < 0 {
		limit = 0
	}
	return &OutputBudget{limit: limit}
}

func (b *OutputBudget) Used() int64 {
	if b == nil {
		return 0
	}
	return b.used.Load()
}

// Consume reserves all size bytes or none. It lets bounded filesystem readers
// participate in the same generation budget as command output.
func (b *OutputBudget) Consume(size int) bool {
	if size < 0 || b == nil {
		return false
	}
	if size == 0 {
		return true
	}
	want := int64(size)
	for {
		used := b.used.Load()
		if want > b.limit-used {
			return false
		}
		if b.used.CompareAndSwap(used, used+want) {
			return true
		}
	}
}

func (b *OutputBudget) reserve(size int) int {
	if b == nil || size <= 0 {
		return 0
	}
	for {
		used := b.used.Load()
		remaining := b.limit - used
		if remaining <= 0 {
			return 0
		}
		reserved := int64(size)
		if reserved > remaining {
			reserved = remaining
		}
		if b.used.CompareAndSwap(used, used+reserved) {
			return int(reserved)
		}
	}
}

type PaneListLimits struct {
	MaxOutputBytes int64
	MaxRows        int
	MaxFieldBytes  int
}

func DefaultPaneListLimits() PaneListLimits {
	return PaneListLimits{
		MaxOutputBytes: defaultCommandOutputBytes,
		MaxRows:        defaultPaneRows,
		MaxFieldBytes:  defaultPaneFieldBytes,
	}
}

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
	budget := NewOutputBudget(defaultCommandOutputBytes)
	out, err := runRawBounded(ctx, budget, defaultCommandOutputBytes, args...)
	return strings.TrimRight(out, "\n"), err
}

func RunCommandBounded(ctx context.Context, budget *OutputBudget, maxBytes int64, name string, args ...string) (string, error) {
	if maxBytes <= 0 {
		return "", ErrOutputLimit
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	output := &boundedOutput{limit: maxBytes, budget: budget, cancel: cancel}
	cmd := exec.CommandContext(commandCtx, name, args...)
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	if output.Overflowed() {
		return "", fmt.Errorf("%s: %w", name, ErrOutputLimit)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	text := output.String()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(text))
	}
	return text, nil
}

func runRawBounded(ctx context.Context, budget *OutputBudget, maxBytes int64, args ...string) (string, error) {
	out, err := RunCommandBounded(ctx, budget, maxBytes, "tmux", args...)
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

type boundedOutput struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int64
	written  int64
	overflow bool
	budget   *OutputBudget
	cancel   context.CancelFunc
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	remaining := w.limit - w.written
	allowed := len(p)
	if remaining < int64(allowed) {
		allowed = max(0, int(remaining))
	}
	if w.budget != nil && allowed > 0 {
		allowed = w.budget.reserve(allowed)
	}
	if allowed > 0 {
		_, _ = w.buf.Write(p[:allowed])
		w.written += int64(allowed)
	}
	if allowed < len(p) {
		w.overflow = true
	}
	overflow := w.overflow
	w.mu.Unlock()
	if overflow {
		w.cancel()
	}
	return len(p), nil
}

func (w *boundedOutput) Overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}

func (w *boundedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func Exec(ctx context.Context, args ...string) error {
	_, err := Run(ctx, args...)
	return err
}

func Display(ctx context.Context, format string) (string, error) {
	return Run(ctx, "display-message", "-p", format)
}

func ListPanes(ctx context.Context) ([]Pane, error) {
	limits := DefaultPaneListLimits()
	return ListPanesBounded(ctx, NewOutputBudget(limits.MaxOutputBytes), limits)
}

func ListPanesBounded(ctx context.Context, budget *OutputBudget, limits PaneListLimits) ([]Pane, error) {
	if limits.MaxOutputBytes <= 0 || limits.MaxRows <= 0 || limits.MaxFieldBytes <= 0 {
		return nil, fmt.Errorf("invalid pane list limits")
	}
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
	out, err := runRawBounded(ctx, budget, limits.MaxOutputBytes, "list-panes", "-a", "-F", format.String())
	if err != nil {
		return nil, err
	}
	return parsePanesBounded(out, limits)
}

func ParsePanes(out string) []Pane {
	var panes []Pane
	for offset := 0; offset < len(out); {
		parts, next, ok := parsePaneFields(out, offset)
		if !ok {
			break
		}
		offset = next
		panes = append(panes, paneFromFields(parts))
	}
	return panes
}

func parsePanesBounded(out string, limits PaneListLimits) ([]Pane, error) {
	panes := make([]Pane, 0, min(limits.MaxRows, 16))
	for offset := 0; offset < len(out); {
		if len(panes) >= limits.MaxRows {
			return nil, fmt.Errorf("pane row limit %d exceeded", limits.MaxRows)
		}
		parts, next, ok := parsePaneFieldsBounded(out, offset, limits.MaxFieldBytes)
		if !ok {
			return nil, fmt.Errorf("malformed pane inventory at byte %d", offset)
		}
		offset = next
		panes = append(panes, paneFromFields(parts))
	}
	return panes, nil
}

func parsePaneFieldsBounded(out string, offset int, maxFieldBytes int) ([]string, int, bool) {
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
		if err != nil || length < 0 || length > maxFieldBytes {
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

func paneFromFields(parts []string) Pane {
	return Pane{
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
	}
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

func CapturePaneBounded(ctx context.Context, budget *OutputBudget, paneID string, lines int, maxBytes int64) (string, error) {
	if !canonicalPaneID(paneID) {
		return "", fmt.Errorf("invalid pane id %q", paneID)
	}
	if lines <= 0 {
		return "", fmt.Errorf("capture lines must be positive")
	}
	start := "-" + strconv.Itoa(lines)
	out, err := runRawBounded(ctx, budget, maxBytes, "capture-pane", "-e", "-p", "-S", start, "-t", paneID)
	return strings.TrimRight(out, "\n"), err
}

// IsCanonicalID reports whether value is a tmux stable ID with no alternate
// decimal spelling, such as $1, @2, or %3.
func IsCanonicalID(value string, prefix byte) bool {
	if len(value) < 2 || value[0] != prefix || len(value) > 2 && value[1] == '0' {
		return false
	}
	for _, digit := range value[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func canonicalPaneID(value string) bool {
	if len(value) < 2 || value[0] != '%' {
		return false
	}
	for _, digit := range value[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func SwitchPane(ctx context.Context, p Pane) error {
	return Exec(ctx,
		"switch-client", "-t", p.SessionID,
		";", "select-window", "-t", p.WindowID,
		";", "select-pane", "-t", p.PaneID,
	)
}
