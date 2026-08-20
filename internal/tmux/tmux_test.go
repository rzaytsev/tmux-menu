package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestRunCommandBoundedFailsAtCapPlusOne(t *testing.T) {
	budget := NewOutputBudget(4096)
	_, err := RunCommandBounded(t.Context(), budget, 64, "sh", "-c", "head -c 65 /dev/zero")
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("cap-plus-one error = %v", err)
	}
	if budget.Used() > 64 {
		t.Fatalf("budget retained %d bytes, want at most 64", budget.Used())
	}
}

func TestOutputBudgetConsumeIsAllOrNothing(t *testing.T) {
	budget := NewOutputBudget(10)
	if !budget.Consume(6) || budget.Consume(5) || budget.Used() != 6 {
		t.Fatalf("budget consume result used=%d", budget.Used())
	}
	if !budget.Consume(4) || budget.Used() != 10 {
		t.Fatalf("budget final consume used=%d", budget.Used())
	}
}

func TestRunCommandBoundedCancelsBlockedCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := RunCommandBounded(ctx, NewOutputBudget(1024), 1024, "sh", "-c", "while :; do :; done")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("blocked command returned err=%v after %s", err, time.Since(started))
	}
}

func TestListPanesBoundedRejectsRowFieldAndOutputOverflow(t *testing.T) {
	valid := framePaneFields("work", "$1", "api", "@2", "1", "0", "%3", "server", "codex", "/tmp/project", "1", "1", "1234", "0", "/tmp/session")
	if _, err := parsePanesBounded(valid+"\n"+valid+"\n", PaneListLimits{MaxRows: 1, MaxFieldBytes: 128}); err == nil {
		t.Fatal("expected pane row overflow")
	}
	oversizedField := framePaneFields("workspace", "$1", "api", "@2", "1", "0", "%3", "server", "codex", "/tmp/project", "1", "1", "1234", "0", "/tmp/session")
	if _, err := parsePanesBounded(oversizedField, PaneListLimits{MaxRows: 2, MaxFieldBytes: 8}); err == nil {
		t.Fatal("expected pane field overflow")
	}

	writeFakeTmux(t, "head -c 129 /dev/zero\n")
	limits := PaneListLimits{MaxOutputBytes: 128, MaxRows: 2, MaxFieldBytes: 128}
	if _, err := ListPanesBounded(t.Context(), NewOutputBudget(256), limits); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("output overflow error = %v", err)
	}
}

func TestCapturePaneBoundedUsesStableTargetStyledOutputAndSharedBudget(t *testing.T) {
	writeFakeTmux(t, "printf '%s\\n' \"$*\"\n")
	budget := NewOutputBudget(256)
	got, err := CapturePaneBounded(t.Context(), budget, "%7", 25, 128)
	if err != nil {
		t.Fatal(err)
	}
	want := "capture-pane -e -p -S -25 -t %7"
	if got != want {
		t.Fatalf("capture args = %q, want %q", got, want)
	}
	if _, err := CapturePaneBounded(t.Context(), budget, "work:1.2", 25, 128); err == nil {
		t.Fatal("non-canonical pane target was accepted")
	}
}

func TestIsCanonicalID(t *testing.T) {
	for _, tc := range []struct {
		value  string
		prefix byte
		want   bool
	}{
		{value: "$0", prefix: '$', want: true},
		{value: "@12", prefix: '@', want: true},
		{value: "%3", prefix: '%', want: true},
		{value: "%03", prefix: '%'},
		{value: "%", prefix: '%'},
		{value: "work", prefix: '$'},
	} {
		if got := IsCanonicalID(tc.value, tc.prefix); got != tc.want {
			t.Fatalf("IsCanonicalID(%q, %q) = %v, want %v", tc.value, tc.prefix, got, tc.want)
		}
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
