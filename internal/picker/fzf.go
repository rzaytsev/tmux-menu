package picker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"tmux-menu/internal/displaytext"
	"tmux-menu/internal/shellquote"
)

type Item[T any] struct {
	Label    string
	Preview  string
	Metadata string
	Disabled bool
	Value    T
}

type Result[T any] struct {
	Values   []T
	Key      string
	Value    T
	Selected bool
	Metadata string
}

type Options struct {
	Multi          bool
	Header         string
	InputHeader    []string
	Input          string
	LiveCommand    string
	RefreshCommand string
	PreviewWindow  string
	Bindings       []string
	InitialIndex   int
}

var ErrCanceled = errors.New("selection canceled")

const emptyItemID = -1

func Select[T any](ctx context.Context, prompt string, items []Item[T]) (T, error) {
	result, err := SelectWithExpect(ctx, prompt, items, nil, "")
	if err != nil {
		var zero T
		return zero, err
	}
	if !result.Selected {
		var zero T
		return zero, ErrCanceled
	}
	return result.Value, nil
}

func SelectWithExpect[T any](ctx context.Context, prompt string, items []Item[T], expectKeys []string, footer string) (Result[T], error) {
	return SelectWithExpectAndPreview(ctx, prompt, items, expectKeys, footer, "")
}

func SelectWithExpectAndPreview[T any](ctx context.Context, prompt string, items []Item[T], expectKeys []string, footer string, previewCommand string) (Result[T], error) {
	return SelectWithExpectAndPreviewOptions(ctx, prompt, items, expectKeys, footer, previewCommand, Options{})
}

func SelectWithExpectAndPreviewOptions[T any](ctx context.Context, prompt string, items []Item[T], expectKeys []string, footer string, previewCommand string, options Options) (Result[T], error) {
	if len(items) == 0 && len(expectKeys) == 0 {
		return Result[T]{}, ErrCanceled
	}
	hasPreview := strings.TrimSpace(previewCommand) != ""
	input, err := EncodeItems(items, hasPreview)
	if err != nil {
		return Result[T]{}, err
	}
	var header strings.Builder
	for _, line := range options.InputHeader {
		prefix := "\t"
		if hasPreview {
			prefix = "\t\t"
		}
		fmt.Fprintln(&header, prefix+displaytext.Line(line))
	}
	args := buildFZFArgs(prompt, expectKeys, footer, previewCommand, options)
	state := StateFromContext(ctx)
	if options.Input != "" {
		input, header = options.Input, strings.Builder{}
	}
	if state != nil {
		args = append(args, "--print-query", "--query", state.Query)
		positionInput := input
		if options.LiveCommand == "" && state.Query != "" && state.Selected != "" {
			fields := "2.."
			if hasPreview {
				fields = "3.."
			}
			positionInput, err = Filter(ctx, input, state.Query, fields, false)
			if err != nil {
				return Result[T]{}, err
			}
		}
		lines := strings.Split(strings.TrimSuffix(positionInput, "\n"), "\n")
		if options.Input != "" && len(lines) >= len(options.InputHeader) {
			lines = lines[len(options.InputHeader):]
		}
		for i, item := range items {
			if state.Selected != "" && state.Selected == itemStateKey(item) {
				for position, line := range lines {
					id, _, _ := strings.Cut(line, "\t")
					if id == strconv.Itoa(i) {
						args = append(args, "--bind", fmt.Sprintf("load:pos(%d)+unbind(load)", position+1))
						break
					}
				}
				break
			}
		}
	}
	cmd := exec.CommandContext(ctx, "fzf", args...)
	cmd.Stdin = strings.NewReader(header.String() + input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return Result[T]{}, classifyFZFError(err, stderr.String())
	}
	output := string(out)
	if state != nil {
		var ok bool
		state.Query, output, ok = strings.Cut(output, "\n")
		if !ok {
			return Result[T]{}, fmt.Errorf("missing fzf query output")
		}
	}
	result, err := parseSelectionOutput(output, items, len(expectKeys) > 0, options.Multi)
	if state != nil && result.Selected {
		line := output
		if len(expectKeys) > 0 {
			_, line, _ = strings.Cut(line, "\n")
		}
		line, _, _ = strings.Cut(line, "\n")
		id, _, _ := strings.Cut(line, "\t")
		index, _ := strconv.Atoi(id)
		state.Selected = itemStateKey(items[index])
	}
	return result, err
}

// EncodeItems keeps opaque selection metadata outside visible labels.
func EncodeItems[T any](items []Item[T], hasPreview bool) (string, error) {
	var input strings.Builder
	if len(items) == 0 {
		if hasPreview {
			fmt.Fprintf(&input, "%d\t\tNo items\n", emptyItemID)
		} else {
			fmt.Fprintf(&input, "%d\tNo items\n", emptyItemID)
		}
	}
	for i, item := range items {
		if hasPreview {
			if strings.ContainsFunc(item.Preview, displaytext.UnsafeControl) {
				return "", fmt.Errorf("item %d preview contains unsupported control characters", i)
			}
			if strings.ContainsFunc(item.Metadata, displaytext.UnsafeControl) {
				return "", fmt.Errorf("item %d metadata contains unsupported control characters", i)
			}
			fmt.Fprintf(&input, "%d\t%s\t%s", i, item.Preview, displaytext.Line(item.Label))
			if item.Metadata != "" {
				fmt.Fprintf(&input, "\t%s", item.Metadata)
			}
			input.WriteByte('\n')
			continue
		}
		fmt.Fprintf(&input, "%d\t%s\n", i, displaytext.Line(item.Label))
	}
	return input.String(), nil
}

func classifyFZFError(err error, stderr string) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		switch exitErr.ExitCode() {
		case 1, 130:
			return ErrCanceled
		}
		stderr = strings.TrimSpace(stderr)
		if stderr != "" {
			return fmt.Errorf("fzf failed: %w: %s", err, stderr)
		}
		return fmt.Errorf("fzf failed: %w", err)
	}
	return fmt.Errorf("run fzf: %w", err)
}

func buildFZFArgs(prompt string, expectKeys []string, footer string, previewCommand string, options Options) []string {
	hasPreview := strings.TrimSpace(previewCommand) != ""
	withNth := "2.."
	if hasPreview {
		withNth = "3.."
	}
	args := []string{
		"--ansi",
		"--height=100%",
		"--layout=reverse",
		"--prompt", prompt,
		"--delimiter", "\t",
		"--with-nth", withNth,
	}
	if options.Multi {
		args = append(args, "--multi", "--no-wrap", "--bind", "tab:toggle+down,btab:toggle+up")
	}
	if len(expectKeys) > 0 {
		args = append(args, "--expect="+strings.Join(expectKeys, ","))
	}
	if footer != "" {
		args = append(args, "--footer", footer)
	}
	if options.Header != "" {
		args = append(args, "--header", displaytext.Line(options.Header))
	}
	if len(options.InputHeader) > 0 {
		args = append(args, "--header-lines", strconv.Itoa(len(options.InputHeader)))
	}
	if options.LiveCommand != "" {
		idleRefresh := `if [ "${FZF_IDLE_TIME_MS:-0}" -ge 500 ]; then ` + options.RefreshCommand + `; fi`
		args = append(args, "--disabled", "--id-nth", "1", "--no-sort", "--with-nth", "3",
			"--bind", "change:bg-cancel+untrack-current+reload("+options.LiveCommand+")",
			"--bind", "every(2):bg-transform:"+idleRefresh,
			"--bind", "resize:bg-cancel+bg-transform:"+options.RefreshCommand)
	}
	if hasPreview {
		args = append(args, "--preview", previewCommandForField(previewCommand, "{s2}"))
	}
	if options.PreviewWindow != "" {
		args = append(args, "--preview-window", options.PreviewWindow)
	}
	if options.InitialIndex > 0 {
		args = append(args, "--bind", fmt.Sprintf("load:pos(%d)+unbind(load)", options.InitialIndex+1))
	}
	for _, binding := range options.Bindings {
		if strings.TrimSpace(binding) == "" {
			continue
		}
		args = append(args, "--bind", binding)
	}
	return args
}

func previewCommandForField(command string, field string) string {
	command = strings.TrimSpace(command)
	if strings.Contains(command, "{}") {
		// fzf quotes field values itself. Keep them outside the configured shell
		// template so a quoted placeholder cannot turn a filename into shell code.
		script := "set -f; IFS=; " + strings.ReplaceAll(command, "{}", "$1")
		return "sh -c " + shellquote.Quote(script) + " tmux-menu " + field
	}
	return command + " " + field
}

func parseFZFOutput[T any](out string, items []Item[T], expect bool) (Result[T], error) {
	selected := strings.TrimRight(out, "\n")
	if selected == "" {
		return Result[T]{}, ErrCanceled
	}
	line := selected
	key := ""
	if expect {
		lines := strings.SplitN(selected, "\n", 2)
		key = lines[0]
		if len(lines) < 2 || lines[1] == "" {
			if key != "" {
				return Result[T]{Key: key}, nil
			}
			return Result[T]{}, ErrCanceled
		}
		line = lines[1]
	}
	if strings.ContainsAny(line, "\n\r\x00") {
		return Result[T]{}, fmt.Errorf("bad fzf output: multiple or malformed records")
	}
	idText, _, ok := strings.Cut(line, "\t")
	if !ok {
		return Result[T]{}, fmt.Errorf("bad fzf output %q", selected)
	}
	id, err := strconv.Atoi(idText)
	if err == nil && id == emptyItemID {
		if key != "" {
			return Result[T]{Key: key}, nil
		}
		return Result[T]{}, ErrCanceled
	}
	if err != nil || id < 0 || id >= len(items) {
		return Result[T]{}, fmt.Errorf("bad fzf item id %q", idText)
	}
	if items[id].Disabled {
		if key != "" {
			return Result[T]{Key: key}, nil
		}
		return Result[T]{}, ErrCanceled
	}
	metadata := ""
	if fields := strings.Split(line, "\t"); len(fields) >= 4 {
		metadata = fields[3]
	}
	return Result[T]{Key: key, Value: items[id].Value, Selected: true, Metadata: metadata}, nil
}

// Parse each selected record through the same strict opaque-ID boundary.
func parseSelectionOutput[T any](out string, items []Item[T], expect, multi bool) (Result[T], error) {
	if !multi {
		return parseFZFOutput(out, items, expect)
	}
	key, body := "", out
	if expect {
		var ok bool
		key, body, ok = strings.Cut(out, "\n")
		if !ok {
			return parseFZFOutput(out, items, expect)
		}
	}
	if strings.TrimSuffix(body, "\n") == "" {
		return parseFZFOutput(out, items, expect)
	}
	var result Result[T]
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		row, err := parseFZFOutput(key+"\n"+line, items, true)
		if err != nil {
			return Result[T]{}, err
		}
		if !result.Selected {
			result = row
			result.Key = key
		}
		if row.Selected {
			result.Values = append(result.Values, row.Value)
		}
	}
	return result, nil
}
