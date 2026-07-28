package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePanes(t *testing.T) {
	line := "work" + sep + "$1" + sep + "api" + sep + "@2" + sep + "1" + sep + "0" + sep + "%3" + sep + "server" + sep + "codex" + sep + "/tmp/project" + sep + "1" + sep + "1" + sep + "1234" + sep + "0"
	panes := ParsePanes(line)
	if len(panes) != 1 {
		t.Fatalf("got %d panes", len(panes))
	}
	p := panes[0]
	if p.SessionID != "$1" || p.WindowID != "@2" || p.PaneID != "%3" {
		t.Fatalf("wrong ids: %#v", p)
	}
	if p.PanePID != "1234" {
		t.Fatalf("wrong pane pid: %#v", p)
	}
	if !p.PaneActive || !p.WindowActive {
		t.Fatalf("active flags not parsed: %#v", p)
	}
	if p.AutomaticRename {
		t.Fatalf("automatic-rename should be disabled: %#v", p)
	}
}

func TestRunIncludesTmuxStderrOnFailure(t *testing.T) {
	writeFakeTmux(t, "printf 'tmux failed\\n' >&2\nexit 2\n")

	_, err := Run(t.Context(), "bad-command")
	if err == nil {
		t.Fatal("expected tmux error")
	}
	if !strings.Contains(err.Error(), "bad-command") || !strings.Contains(err.Error(), "tmux failed") {
		t.Fatalf("tmux error should include command and stderr, got: %v", err)
	}
}

func TestListPanesUsesTmuxCommandPath(t *testing.T) {
	line := "work" + sep + "$1" + sep + "api" + sep + "@2" + sep + "1" + sep + "0" + sep + "%3" + sep + "server" + sep + "codex" + sep + "/tmp/project" + sep + "1" + sep + "1" + sep + "1234" + sep + "1"
	writeFakeTmux(t, "printf '%s\\n' '"+line+"'\n")

	panes, err := ListPanes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 || panes[0].PaneID != "%3" || panes[0].PanePID != "1234" || !panes[0].AutomaticRename {
		t.Fatalf("panes = %#v", panes)
	}
}

func TestCapturePaneBuildsExpectedTmuxArgs(t *testing.T) {
	writeFakeTmux(t, "printf '%s\\n' \"$*\"\n")

	got, err := CapturePane(t.Context(), "%7", 25)
	if err != nil {
		t.Fatal(err)
	}
	want := "capture-pane -p -S -25 -t %7"
	if got != want {
		t.Fatalf("capture args = %q, want %q", got, want)
	}
}

func writeFakeTmux(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}
