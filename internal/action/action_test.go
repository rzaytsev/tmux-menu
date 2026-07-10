package action

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tmux-menu/internal/config"
)

func TestPopupArgsUseWorkingDir(t *testing.T) {
	d := Dispatch{
		Mode:        "popup",
		Cmd:         "lazygit",
		WorkingDir:  "/tmp/project",
		PopupWidth:  "90%",
		PopupHeight: "80%",
		PopupBorder: "rounded",
	}
	got := popupArgs(d)
	want := []string{"display-popup", "-E", "-b", "rounded", "-w", "90%", "-h", "80%", "-d", "/tmp/project", "lazygit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("popup args = %#v, want %#v", got, want)
	}
}

func TestPaneArgsUseOriginPaneAndWorkingDir(t *testing.T) {
	d := Dispatch{
		Mode:       "pane",
		Cmd:        "nvim /tmp/project/todo/task.md",
		WorkingDir: "/tmp/project",
	}
	got := paneArgs(d, "%7")
	want := []string{"split-window", "-t", "%7", "-c", "/tmp/project", "nvim /tmp/project/todo/task.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pane args = %#v, want %#v", got, want)
	}
}

func TestPaneArgsCanSplitRight(t *testing.T) {
	d := Dispatch{
		Mode:            "pane",
		Cmd:             "nvim /tmp/project/bookmark.md",
		WorkingDir:      "/tmp/project",
		SplitHorizontal: true,
	}
	got := paneArgs(d, "%7")
	want := []string{"split-window", "-h", "-t", "%7", "-c", "/tmp/project", "nvim /tmp/project/bookmark.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pane args = %#v, want %#v", got, want)
	}
}

func TestPaneArgsCanSplitLeft(t *testing.T) {
	d := Dispatch{
		Mode:       "pane",
		Cmd:        "nvim /tmp/project/link.md",
		WorkingDir: "/tmp/project",
		PaneSide:   "left",
	}
	got := paneArgs(d, "%7")
	want := []string{"split-window", "-h", "-b", "-t", "%7", "-c", "/tmp/project", "nvim /tmp/project/link.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pane args = %#v, want %#v", got, want)
	}
}

func TestPaneArgsCanSplitAbove(t *testing.T) {
	d := Dispatch{
		Mode:       "pane",
		Cmd:        "nvim /tmp/project/link.md",
		WorkingDir: "/tmp/project",
		PaneSide:   "above",
	}
	got := paneArgs(d, "%7")
	want := []string{"split-window", "-b", "-t", "%7", "-c", "/tmp/project", "nvim /tmp/project/link.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pane args = %#v, want %#v", got, want)
	}
}

func TestFromCommandDefaultsWorkingDirToOriginPath(t *testing.T) {
	got := FromCommand(
		config.Command{Title: "Lazygit", Mode: "popup", Cmd: "lazygit"},
		config.PopupConfig{Width: "90%", Height: "80%", Border: "rounded"},
		"/tmp/repo",
	)
	if got.WorkingDir != "/tmp/repo" {
		t.Fatalf("working dir = %q, want origin path", got.WorkingDir)
	}
}

func TestProjectSessionNameUsesDirectoryName(t *testing.T) {
	got := projectSessionName("/tmp/my.project")
	if got != "my_project" {
		t.Fatalf("session name = %q, want my_project", got)
	}
}

func TestProjectBootstrapCommandSignalsWait(t *testing.T) {
	got := projectBootstrapCommand("/tmp/project root", "/tmp/project root/.tmux-bootstrap", "wait-token")
	want := "cd '/tmp/project root' && bash '/tmp/project root/.tmux-bootstrap'; tmux wait-for -S 'wait-token'"
	if got != want {
		t.Fatalf("bootstrap command = %q, want %q", got, want)
	}
}

func TestExecutePasteUsesNamedBufferAndDeletesIt(t *testing.T) {
	var calls [][]string
	restore := stubActionTmux(t, func(ctx context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		return "", nil
	})
	defer restore()
	newPasteBufferName = func() string { return "tmux-menu-test" }

	err := Execute(context.Background(), Dispatch{Mode: "paste", Cmd: "git status", Enter: true}, "%7")
	if err != nil {
		t.Fatal(err)
	}

	want := [][]string{
		{"set-buffer", "-b", "tmux-menu-test", "--", "git status"},
		{"paste-buffer", "-b", "tmux-menu-test", "-t", "%7"},
		{"send-keys", "-t", "%7", "Enter"},
		{"delete-buffer", "-b", "tmux-menu-test"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("tmux calls = %#v, want %#v", calls, want)
	}
}

func TestExecuteSequenceRunsStepsInOrder(t *testing.T) {
	var shellCalls []string
	oldShellRunner := shellRunner
	defer func() { shellRunner = oldShellRunner }()
	shellRunner = func(ctx context.Context, command string) error {
		shellCalls = append(shellCalls, command)
		return nil
	}
	var tmuxCalls [][]string
	restore := stubActionTmux(t, func(ctx context.Context, args ...string) (string, error) {
		tmuxCalls = append(tmuxCalls, append([]string(nil), args...))
		return "", nil
	})
	defer restore()

	err := Execute(context.Background(), Dispatch{
		Mode: "sequence",
		Steps: []Dispatch{
			{Mode: "shell", Cmd: "printf %s '/tmp/project/main.go' | pbcopy"},
			{Mode: "popup", Cmd: "vim /tmp/project/main.go", WorkingDir: "/tmp/project"},
		},
	}, "%7")
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(shellCalls, []string{"printf %s '/tmp/project/main.go' | pbcopy"}) {
		t.Fatalf("shell calls = %#v", shellCalls)
	}
	wantTmux := [][]string{{"display-popup", "-E", "-B", "-d", "/tmp/project", "vim /tmp/project/main.go"}}
	if !reflect.DeepEqual(tmuxCalls, wantTmux) {
		t.Fatalf("tmux calls = %#v, want %#v", tmuxCalls, wantTmux)
	}
}

func TestExecuteProjectUsesExplicitSessionName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMUX", "1")
	var calls [][]string
	restore := stubActionTmux(t, func(ctx context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(args) >= 1 && args[0] == "has-session" {
			return "", fmt.Errorf("missing")
		}
		return "", nil
	})
	defer restore()

	err := Execute(context.Background(), Dispatch{
		Mode:               "project",
		ProjectPath:        root,
		ProjectSessionName: "api_12345678",
	}, "%1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"has-session", "-t=api_12345678"},
		{"new-session", "-ds", "api_12345678", "-c", root},
		{"switch-client", "-t", "api_12345678"},
	} {
		if !hasCall(calls, want) {
			t.Fatalf("missing tmux call %#v in %#v", want, calls)
		}
	}
}

func TestRunProjectBootstrapTimesOutWaiting(t *testing.T) {
	root := t.TempDir()
	bootstrap := filepath.Join(root, ".tmux-sessionizer")
	if err := os.WriteFile(bootstrap, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	restore := stubActionTmux(t, func(ctx context.Context, args ...string) (string, error) {
		if len(args) >= 1 && args[0] == "list-panes" {
			return "%1\n", nil
		}
		if len(args) >= 1 && args[0] == "wait-for" {
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "", nil
	})
	defer restore()
	projectBootstrapTimeout = 10 * time.Millisecond

	err := runProjectBootstrap(context.Background(), "api", root, ".tmux-sessionizer")
	if err == nil {
		t.Fatal("expected bootstrap timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func stubActionTmux(t *testing.T, fn func(context.Context, ...string) (string, error)) func() {
	t.Helper()
	oldRun := tmuxRun
	oldExec := tmuxExec
	oldPasteBufferName := newPasteBufferName
	oldBootstrapTimeout := projectBootstrapTimeout
	tmuxRun = fn
	tmuxExec = func(ctx context.Context, args ...string) error {
		_, err := fn(ctx, args...)
		return err
	}
	return func() {
		tmuxRun = oldRun
		tmuxExec = oldExec
		newPasteBufferName = oldPasteBufferName
		projectBootstrapTimeout = oldBootstrapTimeout
	}
}

func hasCall(calls [][]string, want []string) bool {
	for _, call := range calls {
		if reflect.DeepEqual(call, want) {
			return true
		}
	}
	return false
}
