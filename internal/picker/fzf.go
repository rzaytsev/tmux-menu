package picker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Item[T any] struct {
	Label    string
	Preview  string
	Disabled bool
	Value    T
}

type Result[T any] struct {
	Key      string
	Value    T
	Selected bool
}

type Options struct {
	PreviewWindow string
	Bindings      []string
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
	lines := make([]string, 0, len(items))
	hasPreview := strings.TrimSpace(previewCommand) != ""
	if len(items) == 0 {
		if hasPreview {
			lines = append(lines, fmt.Sprintf("%d\t\tNo items", emptyItemID))
		} else {
			lines = append(lines, fmt.Sprintf("%d\tNo items", emptyItemID))
		}
	}
	for i, item := range items {
		if hasPreview {
			lines = append(lines, fmt.Sprintf("%d\t%s\t%s", i, item.Preview, item.Label))
			continue
		}
		lines = append(lines, fmt.Sprintf("%d\t%s", i, item.Label))
	}
	args := buildFZFArgs(prompt, expectKeys, footer, previewCommand, options)
	cmd := exec.CommandContext(ctx, "fzf", args...)
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return Result[T]{}, classifyFZFError(err, stderr.String())
	}
	return parseFZFOutput(string(out), items, len(expectKeys) > 0)
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
	if len(expectKeys) > 0 {
		args = append(args, "--expect="+strings.Join(expectKeys, ","))
	}
	if footer != "" {
		args = append(args, "--footer", footer)
	}
	if hasPreview {
		args = append(args, "--preview", previewCommandForField(previewCommand, "{2}"))
	}
	if options.PreviewWindow != "" {
		args = append(args, "--preview-window", options.PreviewWindow)
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
		return strings.ReplaceAll(command, "{}", field)
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
	return Result[T]{Key: key, Value: items[id].Value, Selected: true}, nil
}
