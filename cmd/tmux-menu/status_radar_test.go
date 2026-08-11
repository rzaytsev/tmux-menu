package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tmux-menu/internal/config"
	"tmux-menu/internal/tmux"
)

func TestRunStatusReporterUsesTargetSessionContext(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "report.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '{"blocks":[{"title":"Context","status":"warning","summary":"%s","details":"%s|%s"}]}' "$TMUX_MENU_SESSION_NAME" "$PWD" "$TMUX_MENU_ORIGIN_PANE"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	target := config.StatusTarget{Title: "Work", Session: "work", Command: "./report.sh"}
	session := tmux.Pane{SessionID: "$4", SessionName: "work", SessionPath: root}

	blocks, err := runStatusReporter(context.Background(), target, session, runtimeContext{OriginPane: "%7"})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Summary != "work" {
		t.Fatalf("unexpected reporter blocks: %#v", blocks)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if blocks[0].Details != resolvedRoot+"|%7" {
		t.Fatalf("reporter details = %q, want target cwd and origin pane", blocks[0].Details)
	}
}

func TestCollectStatusRadarSortsByUrgencyAndKeepsMissingSessions(t *testing.T) {
	root := t.TempDir()
	targets := []config.StatusTarget{
		{Title: "Healthy", Session: "healthy", Command: `printf '%s' '{"blocks":[{"title":"Git","status":"ok","summary":"Clean"}]}'`},
		{Title: "Missing", Session: "missing", Command: "unused"},
		{Title: "Needs input", Session: "attention", Command: `printf '%s' '{"blocks":[{"title":"Agents","status":"attention","summary":"Permission required"}]}'`},
	}
	panes := []tmux.Pane{
		{SessionID: "$1", SessionName: "healthy", SessionPath: root},
		{SessionID: "$2", SessionName: "attention", SessionPath: root},
	}

	reports := collectStatusRadar(context.Background(), targets, panes, runtimeContext{}, time.Second)
	if got := []string{reports[0].Target.Session, reports[1].Target.Session, reports[2].Target.Session}; strings.Join(got, ",") != "attention,missing,healthy" {
		t.Fatalf("status order = %#v", got)
	}
	if reports[1].Available || reports[1].Status != "unknown" || reports[1].Summary != "Session is not running" {
		t.Fatalf("missing session report = %#v", reports[1])
	}
	if !reports[0].Available || reports[0].Session.SessionID != "$2" {
		t.Fatalf("attention report lost stable session: %#v", reports[0])
	}
}

func TestCollectStatusRadarShowsReporterTimeout(t *testing.T) {
	target := config.StatusTarget{Title: "Slow", Session: "slow", Command: "sleep 1"}
	panes := []tmux.Pane{{SessionID: "$1", SessionName: "slow", SessionPath: t.TempDir()}}

	reports := collectStatusRadar(context.Background(), []config.StatusTarget{target}, panes, runtimeContext{}, 20*time.Millisecond)
	if len(reports) != 1 || reports[0].Status != "unknown" || !strings.Contains(reports[0].Summary, "timed out after 20ms") {
		t.Fatalf("timeout report = %#v", reports)
	}
}

func TestRunStatusReporterRejectsInvalidBlockStatus(t *testing.T) {
	target := config.StatusTarget{
		Title:   "Bad",
		Session: "bad",
		Command: `printf '%s' '{"blocks":[{"title":"Git","status":"broken","summary":"Nope"}]}'`,
	}
	_, err := runStatusReporter(context.Background(), target, tmux.Pane{SessionPath: t.TempDir()}, runtimeContext{})
	if err == nil || !strings.Contains(err.Error(), "must be one of attention, warning, ok, unknown") {
		t.Fatalf("runStatusReporter() error = %v", err)
	}
}

func TestRunStatusReporterRequiresTargetSessionPath(t *testing.T) {
	target := config.StatusTarget{Title: "Bad", Session: "bad", Command: "exit 0"}
	_, err := runStatusReporter(context.Background(), target, tmux.Pane{}, runtimeContext{})
	if err == nil || !strings.Contains(err.Error(), "session path is unavailable") {
		t.Fatalf("runStatusReporter() error = %v", err)
	}
}

func TestStatusRadarOutputLimitsCapturedBytes(t *testing.T) {
	output := statusRadarOutput{Limit: 4}
	written, err := output.Write([]byte("abcdef"))
	if err != nil || written != 6 || output.String() != "abcd" || !output.Truncated {
		t.Fatalf("limited output = %q, written %d, truncated %t, error %v", output.String(), written, output.Truncated, err)
	}
}

func TestStatusRadarItemsWriteBlockPreviewsAndSwitchBySessionID(t *testing.T) {
	previewDir := t.TempDir()
	reports := []statusRadarReport{
		{
			Target:    config.StatusTarget{Title: "Work", Session: "work"},
			Session:   tmux.Pane{SessionID: "$8", SessionName: "work", SessionPath: "/tmp/work"},
			Blocks:    []statusRadarBlock{{Title: "Agents", Status: "attention", Summary: "Permission required", Details: "pane %3"}},
			Status:    "attention",
			Summary:   "Permission required",
			Available: true,
		},
		{
			Target:  config.StatusTarget{Title: "Missing", Session: "missing"},
			Blocks:  []statusRadarBlock{{Title: "Reporter", Status: "unknown", Summary: "Session is not running"}},
			Status:  "unknown",
			Summary: "Session is not running",
		},
	}

	items, err := statusRadarItems(reports, previewDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Value.dispatch.Mode != "switch-session" || items[0].Value.dispatch.SessionID != "$8" {
		t.Fatalf("radar dispatch = %#v", items)
	}
	if !items[1].Disabled {
		t.Fatal("missing session row should be display-only")
	}
	preview, err := os.ReadFile(items[0].Preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Work", "Agents", "Permission required", "pane %3"} {
		if !strings.Contains(stripANSI(string(preview)), want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
}

func TestStatusRadarRollupUsesTopTwoProblemSummaries(t *testing.T) {
	status, summary := statusRadarRollup([]statusRadarBlock{
		{Status: "warning", Summary: "Dirty worktree"},
		{Status: "attention", Summary: "Permission required"},
		{Status: "unknown", Summary: "Deploy unavailable"},
		{Status: "ok", Summary: "Tests pass"},
	})
	if status != "attention" || summary != "Permission required · Dirty worktree · +1 more" {
		t.Fatalf("rollup = %q, %q", status, summary)
	}
}
