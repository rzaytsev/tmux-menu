package picker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFZFOutputWithExpectedKey(t *testing.T) {
	items := []Item[string]{{Label: "one", Value: "selected"}}
	got, err := parseFZFOutput("alt-2\n0\tone\n", items, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "alt-2" {
		t.Fatalf("key = %q, want alt-2", got.Key)
	}
	if !got.Selected || got.Value != "selected" {
		t.Fatalf("expected key and selected item, got %#v", got)
	}
}

func TestParseFZFOutputWithExpectedKeyAndNoSelection(t *testing.T) {
	got, err := parseFZFOutput[string]("alt-1\n", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "alt-1" || got.Selected {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestParseFZFOutputWithExpectedKeyAndEmptyPlaceholder(t *testing.T) {
	got, err := parseFZFOutput[string]("tab\n-1\tNo items\n", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "tab" || got.Selected {
		t.Fatalf("unexpected empty picker navigation result: %#v", got)
	}
}

func TestParseFZFOutputTreatsEmptyPlaceholderEnterAsCanceled(t *testing.T) {
	_, err := parseFZFOutput[string]("\n-1\tNo items\n", nil, true)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("empty placeholder Enter should cancel, got: %v", err)
	}
}

func TestParseFZFOutputTreatsDisabledItemAsCanceled(t *testing.T) {
	items := []Item[string]{{Label: "session", Disabled: true}}
	_, err := parseFZFOutput("\n0\tsession\n", items, true)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("disabled item Enter should cancel, got: %v", err)
	}
}

func TestParseFZFOutputKeepsExpectedKeyFromDisabledItem(t *testing.T) {
	items := []Item[string]{{Label: "session", Disabled: true}}
	got, err := parseFZFOutput("tab\n0\tsession\n", items, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "tab" || got.Selected {
		t.Fatalf("unexpected disabled-item navigation result: %#v", got)
	}
}

func TestParseFZFOutputWithExpectedSelection(t *testing.T) {
	items := []Item[string]{{Label: "one", Value: "selected"}}
	got, err := parseFZFOutput("\n0\tone\n", items, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "" || !got.Selected || got.Value != "selected" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestBuildFZFArgsAddsPreviewForHiddenPreviewField(t *testing.T) {
	args := buildFZFArgs("status> ", []string{"alt-1"}, "Alt-1 main", "glow", Options{})
	got := strings.Join(args, "\n")
	for _, want := range []string{
		"--delimiter\n\t",
		"--with-nth\n3..",
		"--footer\nAlt-1 main",
		"--preview\nglow {s2}",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fzf args missing %q:\n%s", want, got)
		}
	}
}

func TestBuildFZFArgsCanHidePreviewBehindSpace(t *testing.T) {
	args := buildFZFArgs("status> ", nil, "", "glow", Options{
		PreviewWindow: "right:60%:hidden:wrap",
		Bindings:      []string{"space:toggle-preview"},
	})
	got := strings.Join(args, "\n")
	for _, want := range []string{
		"--preview-window\nright:60%:hidden:wrap",
		"--bind\nspace:toggle-preview",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fzf args missing %q:\n%s", want, got)
		}
	}
}

func TestBuildFZFArgsPositionsInitialSelection(t *testing.T) {
	args := buildFZFArgs("agents> ", []string{"ctrl-r"}, "", "preview", Options{InitialIndex: 2})
	got := strings.Join(args, "\n")
	if !strings.Contains(got, "--bind\nload:pos(3)") {
		t.Fatalf("initial picker position missing from args:\n%s", got)
	}
}

func TestPickerPreservesQueryAndSelectionAcrossReorderedRows(t *testing.T) {
	writeFakeFZF(t, "if [ \"$1\" = --filter ]; then /bin/cat; exit; fi\nprintf 'long query\\nctrl-r\\n1\\t%%2\\tsecond\\texact-token\\n'\n")
	state := &State{Query: "previous query", Selected: "%2"}
	ctx := WithState(t.Context(), state)
	items := []Item[string]{{Label: "first", Preview: "%1", Value: "first"}, {Label: "second", Preview: "%2", Value: "second"}}
	result, err := SelectWithExpectAndPreview(ctx, "agents> ", items, []string{"ctrl-r"}, "", "preview")
	if err != nil || result.Value != "second" || result.Metadata != "exact-token" || state.Query != "long query" || state.Selected != "%2" {
		t.Fatalf("result=%+v state=%+v error=%v", result, state, err)
	}
	writeFakeFZF(t, "if [ \"$1\" = --filter ]; then /bin/cat; exit; fi\nprintf '%s\\n' \"$@\" > \"$PICKER_ARGS\"\nprintf 'long query\\nctrl-r\\n0\\t%%2\\tsecond\\n'\n")
	path := filepath.Join(t.TempDir(), "args")
	t.Setenv("PICKER_ARGS", path)
	items[0], items[1] = items[1], items[0]
	if _, err := SelectWithExpectAndPreview(ctx, "agents> ", items, []string{"ctrl-r"}, "", "preview"); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(path)
	if !strings.Contains(string(args), "--query\nlong query") || !strings.Contains(string(args), "load:pos(1)+unbind(load)") {
		t.Fatalf("query or stable selection lost: %s", args)
	}
}

func TestBuildFZFArgsKeepsColumnHeaderSeparateFromSelectableItems(t *testing.T) {
	args := buildFZFArgs("agents> ", nil, "", "preview", Options{Header: "STATUS\nSESSION\x1b]52;c;secret\a"})
	for i, arg := range args {
		if arg == "--header" {
			if !strings.Contains(args[i+1], "STATUS SESSION") || strings.Contains(args[i+1], "secret") || strings.ContainsAny(args[i+1], "\n\a") {
				t.Fatalf("unsafe header: %q", args[i+1])
			}
			return
		}
	}
	t.Fatal("missing pinned header")
}

func TestSelectWithExpectReturnsMissingFZFError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := SelectWithExpect(context.Background(), "test> ", []Item[string]{{Label: "one", Value: "1"}}, nil, "")
	if err == nil {
		t.Fatal("expected missing fzf error")
	}
	if errors.Is(err, ErrCanceled) {
		t.Fatalf("missing fzf should not be treated as cancellation: %v", err)
	}
}

func TestSelectWithExpectKeepsEmptyPickerNavigable(t *testing.T) {
	writeFakeFZF(t, "cat >/dev/null\nprintf 'tab\\n-1\\tNo items\\n'\n")

	got, err := SelectWithExpect[string](context.Background(), "test> ", nil, []string{"tab"}, "Tab:Next")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "tab" || got.Selected {
		t.Fatalf("unexpected empty picker result: %#v", got)
	}
}

func TestSelectWithExpectReturnsFZFStderrOnRuntimeError(t *testing.T) {
	writeFakeFZF(t, "printf 'bad terminal\\n' >&2\nexit 2\n")

	_, err := SelectWithExpect(context.Background(), "test> ", []Item[string]{{Label: "one", Value: "1"}}, nil, "")
	if err == nil {
		t.Fatal("expected fzf runtime error")
	}
	if errors.Is(err, ErrCanceled) {
		t.Fatalf("fzf runtime error should not be treated as cancellation: %v", err)
	}
	if !strings.Contains(err.Error(), "bad terminal") {
		t.Fatalf("fzf error should include stderr, got: %v", err)
	}
}

func TestSelectWithExpectTreatsFZFCancelExitAsCanceled(t *testing.T) {
	writeFakeFZF(t, "exit 1\n")

	_, err := SelectWithExpect(context.Background(), "test> ", []Item[string]{{Label: "one", Value: "1"}}, nil, "")
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("fzf cancel exit should be ErrCanceled, got: %v", err)
	}
}

func writeFakeFZF(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fzf")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}
