package tmux

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParsePanes(t *testing.T) {
	line := framePaneFields("work", "$1", "api", "@2", "1", "0", "%3", "server", "codex", "/tmp/project", "1", "1", "1234", "0", "/tmp/session")
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
	if p.SessionPath != "/tmp/session" {
		t.Fatalf("wrong session path: %#v", p)
	}
	if !p.PaneActive || !p.WindowActive {
		t.Fatalf("active flags not parsed: %#v", p)
	}
	if p.AutomaticRename {
		t.Fatalf("automatic-rename should be disabled: %#v", p)
	}
}

func TestParsePanesLengthFramingPreservesSeparatorsNewlinesAndUTF8(t *testing.T) {
	first := framePaneFields("wörk"+sep+"team", "$1", "api\nwindow", "@2", "1", "0", "%3", "server\nready", "codex", "/tmp/with"+sep+"separator", "1", "1", "1234", "0", "/tmp/session")
	second := framePaneFields("docs", "$2", "notes", "@3", "2", "1", "%4", "ready", "claude", "/tmp/docs", "0", "0", "5678", "1", "/tmp/docs")
	panes := ParsePanes(first + "\n" + second + "\n")
	if len(panes) != 2 {
		t.Fatalf("got %d panes: %#v", len(panes), panes)
	}
	if panes[0].SessionName != "wörk"+sep+"team" || panes[0].WindowName != "api\nwindow" || panes[0].PaneTitle != "server\nready" || panes[0].CurrentPath != "/tmp/with"+sep+"separator" {
		t.Fatalf("framed metadata shifted or changed fields: %#v", panes[0])
	}
	if panes[1].SessionID != "$2" || panes[1].WindowID != "@3" || panes[1].PaneID != "%4" || panes[1].PanePID != "5678" {
		t.Fatalf("second framed identity shifted: %#v", panes[1])
	}
}

func TestParsePanesRejectsMalformedFramingWithoutResynchronizing(t *testing.T) {
	valid := framePaneFields("work", "$1", "api", "@2", "1", "0", "%3", "server", "codex", "/tmp/project", "1", "1", "1234", "0", "/tmp/session")
	malformed := "4" + sep + "work" + "2" + sep + "$1"
	for name, input := range map[string]string{
		"legacy separator record": strings.Join([]string{"work", "$1", "api", "@2", "1", "0", "%3", "server", "codex", "/tmp/project", "1", "1", "1234", "0", "/tmp/session"}, sep),
		"extra field":             valid + "1" + sep + "x",
		"truncated field":         valid + "\n" + malformed,
	} {
		t.Run(name, func(t *testing.T) {
			panes := ParsePanes(input)
			want := 0
			if name == "truncated field" {
				want = 1
			}
			if len(panes) != want {
				t.Fatalf("ParsePanes() returned %d rows, want %d: %#v", len(panes), want, panes)
			}
		})
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
	line := framePaneFields("work", "$1", "api", "@2", "1", "0", "%3", "server", "codex", "/tmp/project", "1", "1", "1234", "1", "/tmp/session")
	writeFakeTmux(t, "case \"$*\" in\n*'#{n:session_name}'*'#{n:pane_id}'*) printf '%s\\n' '"+line+"' ;;\n*) printf 'missing length framing: %s\\n' \"$*\" >&2; exit 2 ;;\nesac\n")

	panes, err := ListPanes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 || panes[0].PaneID != "%3" || panes[0].PanePID != "1234" || panes[0].SessionPath != "/tmp/session" || !panes[0].AutomaticRename {
		t.Fatalf("panes = %#v", panes)
	}
}

func framePaneFields(fields ...string) string {
	var framed strings.Builder
	for _, field := range fields {
		framed.WriteString(strconv.Itoa(len(field)))
		framed.WriteString(sep)
		framed.WriteString(field)
	}
	return framed.String()
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
