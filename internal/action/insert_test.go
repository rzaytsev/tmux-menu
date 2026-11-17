package action

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"tmux-menu/internal/tmux"
)

func insertFixture() (Dispatch, tmux.Pane) {
	d := Dispatch{Mode: "insert", SessionID: "$1", WindowID: "@2", PaneID: "%3", PanePID: 404,
		ForegroundCommand: "agent", WorkingDir: "/tmp/project", Cmd: "path space '$(literal)'"}
	p := tmux.Pane{SessionID: "$1", WindowID: "@2", PaneID: "%3", PanePID: "404", CurrentCommand: "agent", CurrentPath: "/tmp/project"}
	return d, p
}

func TestInsertUsesBracketedLiteralPasteWithoutSubmit(t *testing.T) {
	d, p := insertFixture()
	defer stubActionPaneList(t, []tmux.Pane{p})()
	oldName := newPasteBufferName
	newPasteBufferName = func() string { return "insert-test" }
	defer func() { newPasteBufferName = oldName }()
	var mutations [][]string
	defer stubActionTmux(t, func(_ context.Context, args ...string) (string, error) {
		if args[0] == "display-message" {
			return "1:0", nil
		}
		mutations = append(mutations, append([]string(nil), args...))
		return "", nil
	})()
	if err := Execute(context.Background(), d, "%wrong"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"set-buffer", "-b", "insert-test", "--", d.Cmd},
		{"paste-buffer", "-p", "-r", "-b", "insert-test", "-t", "%3"},
		{"delete-buffer", "-b", "insert-test"},
	}
	if !reflect.DeepEqual(mutations, want) {
		t.Fatalf("calls = %#v, want %#v", mutations, want)
	}
}

func TestInsertRejectsUnsafePayloadsAndChangedOrigins(t *testing.T) {
	for _, scenario := range []string{"newline", "tab", "escape", "invalid utf8", "large", "enter", "moved", "pid", "command", "cwd", "no bracketed paste", "copy mode"} {
		t.Run(scenario, func(t *testing.T) {
			d, p := insertFixture()
			mode := "1:0"
			switch scenario {
			case "newline":
				d.Cmd = "path\ncommand"
			case "tab":
				d.Cmd = "path\tcommand"
			case "escape":
				d.Cmd = "\x1b[201~"
			case "invalid utf8":
				d.Cmd = string([]byte{0xff})
			case "large":
				d.Cmd = strings.Repeat("x", (64<<10)+1)
			case "enter":
				d.Enter = true
			case "moved":
				p.WindowID = "@4"
			case "pid":
				p.PanePID = "405"
			case "command":
				p.CurrentCommand = "zsh"
			case "cwd":
				p.CurrentPath = "/tmp/other"
			case "no bracketed paste":
				mode = "0:0"
			case "copy mode":
				mode = "1:1"
			}
			defer stubActionPaneList(t, []tmux.Pane{p})()
			defer stubActionTmux(t, func(_ context.Context, args ...string) (string, error) {
				if args[0] == "display-message" {
					return mode, nil
				}
				t.Fatalf("invalid insertion mutated tmux: %#v", args)
				return "", nil
			})()
			if err := Execute(context.Background(), d, ""); err == nil {
				t.Fatal("unsafe insertion accepted")
			}
		})
	}
}

func TestInsertRevalidatesAfterBufferAndCleansUpFailure(t *testing.T) {
	for _, failure := range []string{"pane replaced", "paste failed"} {
		t.Run(failure, func(t *testing.T) {
			d, p := insertFixture()
			oldList := tmuxListPanes
			defer func() { tmuxListPanes = oldList }()
			created, deleted, pasted := false, false, false
			tmuxListPanes = func(context.Context) ([]tmux.Pane, error) {
				if created && failure == "pane replaced" {
					p.PanePID = "405"
				}
				return []tmux.Pane{p}, nil
			}
			defer stubActionTmux(t, func(_ context.Context, args ...string) (string, error) {
				switch args[0] {
				case "display-message":
					return "1:0", nil
				case "set-buffer":
					created = true
				case "delete-buffer":
					deleted = true
				case "paste-buffer":
					pasted = true
					return "", errors.New("fixture failure")
				default:
					t.Fatalf("unexpected mutation: %#v", args)
				}
				return "", nil
			})()
			if err := Execute(context.Background(), d, ""); err == nil || !created || !deleted || pasted != (failure == "paste failed") {
				t.Fatalf("err=%v created=%t deleted=%t pasted=%t", err, created, deleted, pasted)
			}
		})
	}
}
