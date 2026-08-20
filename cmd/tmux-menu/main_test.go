package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
	"tmux-menu/internal/tmux"
)

func TestCleanPaneTitleDropsLocalUserHostPrefix(t *testing.T) {
	t.Setenv("USER", "alice")
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	got := cleanPaneTitle("alice@" + host + ":~/projects/tmux-menu")
	if strings.Contains(got, "alice@") || strings.Contains(got, host) {
		t.Fatalf("local host leaked into title: %q", got)
	}
	if got != "~/projects/tmux-menu" {
		t.Fatalf("unexpected cleaned title: %q", got)
	}
}

func TestAgentProcessSnapshotBoundedFailsClosedAtSharedOutputCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ps")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nhead -c 65 /dev/zero\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	snapshot := agentProcessSnapshotBounded(t.Context(), tmux.NewOutputBudget(64), 64)
	if snapshot.available || len(snapshot.roots) != 0 {
		t.Fatalf("overflowing process inventory must fail closed: %#v", snapshot)
	}
}

func TestParseProcessListBoundedRejectsRowAndFieldOverflow(t *testing.T) {
	valid := "100 1 S codex"
	if _, ok := parseProcessListBounded(valid+"\n"+valid+"\n", 1, 64); ok {
		t.Fatal("process row overflow was accepted")
	}
	if _, ok := parseProcessListBounded("100 1 S "+strings.Repeat("x", 65), 4, 64); ok {
		t.Fatal("process field overflow was accepted")
	}
}

func TestCleanPaneTitleDropsShortLocalHostPrefix(t *testing.T) {
	t.Setenv("USER", "alice")
	got := cleanPaneTitle("alice@workstation:~/projects/tmux-menu")
	if strings.Contains(got, "alice@") || strings.Contains(got, "workstation") {
		t.Fatalf("local host leaked into title: %q", got)
	}
	if got != "~/projects/tmux-menu" {
		t.Fatalf("unexpected cleaned title: %q", got)
	}
}

func TestPaneLabelPreservesTitleWhenAutomaticRenameIsEnabled(t *testing.T) {
	label := stripANSI(paneLabel(tmux.Pane{
		SessionName:     "notes",
		WindowName:      "notes-todo",
		WindowIndex:     "2",
		PaneIndex:       "1",
		PaneTitle:       "todo.md",
		CurrentCommand:  "nvim",
		AutomaticRename: true,
	}, ""))

	if strings.Contains(label, "notes-todo |") || !strings.Contains(label, "  todo.md  ") {
		t.Fatalf("automatic rename should preserve the existing pane label: %q", label)
	}
}

func TestPaneLabelIncludesManualWindowName(t *testing.T) {
	label := stripANSI(paneLabel(tmux.Pane{
		SessionName:    "notes",
		WindowName:     "notes-todo",
		WindowIndex:    "2",
		PaneIndex:      "1",
		PaneTitle:      "todo.md",
		CurrentCommand: "nvim",
	}, ""))

	if !strings.Contains(label, "notes-todo | todo.md") {
		t.Fatalf("manual window name should prefix the pane title: %q", label)
	}
}

func TestPaneLabelsRepeatManualWindowNameWithDistinctPaneTitles(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "notes", WindowName: "notes-todo", WindowIndex: "2", PaneIndex: "1", PaneTitle: "todo.md", CurrentCommand: "nvim"},
		{SessionName: "notes", WindowName: "notes-todo", WindowIndex: "2", PaneIndex: "2", PaneTitle: "shell", CurrentCommand: "zsh"},
	}
	items := paneItems(panes, "")
	first := stripANSI(items[0].Label)
	second := stripANSI(items[1].Label)

	if !strings.Contains(first, "notes-todo | todo.md") || !strings.Contains(second, "notes-todo | shell") {
		t.Fatalf("manual window name and distinct pane titles were not retained:\n%s\n%s", first, second)
	}
}

func TestPaneLabelDoesNotDuplicateIdenticalManualWindowName(t *testing.T) {
	label := stripANSI(paneLabel(tmux.Pane{
		SessionName:    "notes",
		WindowName:     "todo.md",
		WindowIndex:    "2",
		PaneIndex:      "1",
		PaneTitle:      "todo.md",
		CurrentCommand: "nvim",
	}, ""))

	if strings.Contains(label, "todo.md | todo.md") || strings.Count(label, "todo.md") != 1 {
		t.Fatalf("identical window and pane titles should not be duplicated: %q", label)
	}
}

func TestPaletteShowsSessionsAndPanesOnly(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", WindowIndex: "1", PaneIndex: "1", PaneID: "%1", CurrentCommand: "zsh"},
		{SessionName: "work", SessionID: "$1", WindowIndex: "1", PaneIndex: "2", PaneID: "%2", PanePID: "1234", CurrentCommand: "codex", CurrentPath: "/tmp/project"},
	}
	items := paletteItemsWithAgentSource(panes, "$1", "", []string{"sessions", "panes"}, nil)
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
	}
	if len(labels) != 3 {
		t.Fatalf("expected session and pane labels only: %#v", labels)
	}
	if !strings.HasPrefix(labels[0], "session") ||
		!strings.HasPrefix(labels[1], "pane") ||
		!strings.HasPrefix(labels[2], "pane") {
		t.Fatalf("expected sessions, then panes:\n%s", strings.Join(labels, "\n"))
	}
	for _, label := range labels {
		if strings.HasPrefix(label, "agent") ||
			strings.HasPrefix(label, "cmd") ||
			strings.HasPrefix(label, "dir") {
			t.Fatalf("palette should not include agent or tools rows:\n%s", strings.Join(labels, "\n"))
		}
	}
}

func TestPaletteUsesConfiguredSectionOrder(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", WindowIndex: "1", PaneIndex: "1", PaneID: "%1", CurrentCommand: "zsh"},
	}
	agentRows := []picker.Item[menuItem]{{
		Label: "agent    work/1.2  codex  working",
	}}
	items := paletteItemsWithAgentSource(panes, "$1", "", []string{"panes", "agents", "sessions"}, func() []picker.Item[menuItem] {
		return agentRows
	})
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
	}
	if len(labels) != 3 {
		t.Fatalf("expected pane, agent, session labels: %#v", labels)
	}
	if !strings.HasPrefix(labels[0], "pane") ||
		!strings.HasPrefix(labels[1], "agent") ||
		!strings.HasPrefix(labels[2], "session") {
		t.Fatalf("wrong configured palette order:\n%s", strings.Join(labels, "\n"))
	}
}

func TestAgentsShowsOnlyAgentPanesWithStatus(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", WindowIndex: "1", PaneIndex: "1", PaneID: "%1", PanePID: "1000", CurrentCommand: "zsh"},
		{SessionName: "work", SessionID: "$1", WindowIndex: "1", PaneIndex: "2", PaneID: "%2", PanePID: "1234", CurrentCommand: "codex", PaneTitle: "tmux-menu|Working", CurrentPath: "/tmp/project"},
		{SessionName: "docs", SessionID: "$2", WindowIndex: "3", PaneIndex: "1", PaneID: "%3", PanePID: "2000", CurrentCommand: "vim"},
	}
	items := agentItemsWithProcessSnapshot(panes, "", processSnapshot{
		roots:    map[int]bool{1234: true},
		statuses: map[int]agentStatus{1234: agentStatusWorking},
		names:    map[int]string{1234: "codex"},
	})
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
	}
	if len(labels) != 1 {
		t.Fatalf("expected one agent label: %#v", labels)
	}
	if !strings.HasPrefix(labels[0], "● > work") ||
		!strings.Contains(labels[0], "work") ||
		strings.Contains(labels[0], "work/1.2") ||
		strings.Contains(labels[0], "working") {
		t.Fatalf("unexpected agent label: %q", labels[0])
	}
	if items[0].Preview != "%2" {
		t.Fatalf("agent preview target = %q, want %%2", items[0].Preview)
	}
}

func TestAgentListShowsEveryAgentAsASelectableRow(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", WindowID: "@1", PaneID: "%1", PanePID: "100", CurrentCommand: "node", PaneTitle: "tmux-menu|Flatten agents|Ready", CurrentPath: "/tmp/tmux-menu"},
		{SessionName: "docs", SessionID: "$2", WindowID: "@2", PaneID: "%3", PanePID: "300", CurrentCommand: "zsh", PaneTitle: "shell", CurrentPath: "/tmp/docs"},
		{SessionName: "work", SessionID: "$1", WindowID: "@3", PaneID: "%2", PanePID: "200", CurrentCommand: "2.1.217", PaneTitle: "✳ Review infrastructure", CurrentPath: "/tmp/infra"},
		{SessionName: "docs", SessionID: "$2", WindowID: "@4", PaneID: "%4", PanePID: "400", CurrentCommand: "codex", PaneTitle: "Docs|Working", CurrentPath: "/tmp/docs"},
	}
	snapshot := processSnapshot{
		roots:    map[int]bool{100: true, 200: true, 400: true},
		statuses: map[int]agentStatus{100: agentStatusWaiting, 200: agentStatusWorking, 400: agentStatusWorking},
		names:    map[int]string{100: "codex", 200: "claude", 400: "codex"},
	}
	rows := agentRowsForPicker(agentRowsForPanes(panes, snapshot, nil))
	items := agentItemsForRows(rows, "%2", map[string]string{"$1": "green", "$2": "blue"}, config.Default().Agents, true)

	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
	}
	want := []string{
		"docs ● > Docs  /tmp/docs",
		"work ○ > Flatten agents  /tmp/tmux-menu",
		"work ○ ✳ *Review infrastructure  /tmp/infra",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("agent list:\n%s\nwant:\n%s", strings.Join(labels, "\n"), strings.Join(want, "\n"))
	}
	for index, paneID := range []string{"%4", "%1", "%2"} {
		if items[index].Disabled || items[index].Preview != paneID || items[index].Value.dispatch.Mode != "switch-pane" || items[index].Value.dispatch.PaneID != paneID {
			t.Fatalf("agent row %d should select pane %s: %#v", index, paneID, items[index])
		}
	}
	if !strings.Contains(items[0].Label, ansiBold+ansiBlue+"docs") || !strings.Contains(items[1].Label, ansiBold+ansiGreen+"work") {
		t.Fatalf("session labels should retain configured colors: %q / %q", items[0].Label, items[1].Label)
	}
}

func TestAgentListWorkdirDropsProjectsPrefix(t *testing.T) {
	t.Setenv("HOME", "/home/alice")
	if got := agentListWorkdir("/home/alice/projects/tmux-menu"); got != "tmux-menu" {
		t.Fatalf("agentListWorkdir() = %q, want tmux-menu", got)
	}
	if got := agentListWorkdir("/home/alice/work/tmux-menu"); got != "~/work/tmux-menu" {
		t.Fatalf("non-project workdir should retain its home-relative path, got %q", got)
	}
}

func TestAgentListClaudeMarkerOnlyFallsBackToWorkdirName(t *testing.T) {
	label := stripANSI(agentListPaneLabel(tmux.Pane{
		SessionName: "work",
		PaneTitle:   "✳",
		CurrentPath: "/tmp/infrastructure",
	}, "", agentStatusWaiting, "claude", "cyan"))

	if !strings.Contains(label, "✳ infrastructure") || strings.Contains(label, "✳ ✳") {
		t.Fatalf("Claude marker-only row should use its workdir name: %q", label)
	}
}

func TestAgentListUsesConfiguredIconsAndColors(t *testing.T) {
	agentsConfig := config.Default().Agents
	agentsConfig.Icons.Codex = "C"
	agentsConfig.Icons.Claude = "L"
	agentsConfig.Icons.Other = "A"
	agentsConfig.Icons.Current = "@"
	agentsConfig.Icons.Attention = "a"
	agentsConfig.Icons.Working = "w"
	agentsConfig.Icons.Waiting = "i"
	agentsConfig.Icons.Unknown = "u"
	agentsConfig.Colors.Codex = "bright_blue"
	agentsConfig.Colors.Claude = "bright_yellow"
	agentsConfig.Colors.Other = "bright_magenta"
	agentsConfig.Colors.Thread = "bright_white"
	agentsConfig.Colors.Workdir = "white"
	agentsConfig.Colors.Attention = "bright_red"
	agentsConfig.Colors.Working = "bright_green"
	agentsConfig.Colors.Waiting = "yellow"
	agentsConfig.Colors.Unknown = "dim"

	pane := tmux.Pane{SessionName: "work", PaneID: "%1", PaneTitle: "thread", CurrentPath: "/tmp/project"}
	label := agentListPaneLabelWithConfig(pane, "%1", agentStatusWaiting, "codex", "red", agentsConfig)
	if got := stripANSI(label); got != "work i C @thread  /tmp/project" {
		t.Fatalf("custom agent list label = %q", got)
	}
	for _, want := range []string{
		ansiBold + ansiRed + "work",
		ansiYellow + "i",
		ansiBrightBlue + "C",
		ansiBold + ansiBrightWhite + "@thread",
		ansiWhite + "/tmp/project",
	} {
		if !strings.Contains(label, want) {
			t.Fatalf("custom agent list label missing %q: %q", want, label)
		}
	}

	if got := stripANSI(agentListPaneLabelWithConfig(pane, "", agentStatusWorking, "claude", "red", agentsConfig)); !strings.HasPrefix(got, "work w L thread") {
		t.Fatalf("custom Claude label = %q", got)
	}
	if got := stripANSI(colorAgentIcon("gemini", agentsConfig)); got != "A" {
		t.Fatalf("custom fallback agent icon = %q", got)
	}
	if got := stripANSI(colorAgentStatusWithConfig(agentStatusAttention, agentsConfig)); got != "a" {
		t.Fatalf("custom attention icon = %q", got)
	}
	if got := stripANSI(colorAgentStatusWithConfig(agentStatusUnknown, agentsConfig)); got != "u" {
		t.Fatalf("custom unknown icon = %q", got)
	}
}

func TestAgentPreviewWindowLeavesTwelveVisibleResults(t *testing.T) {
	if got := agentPreviewWindow(40); got != "down:24:border-rounded:wrap:follow" {
		t.Fatalf("agentPreviewWindow(40) = %q", got)
	}
	if got := agentPreviewWindow(0); got != "down:60%:border-rounded:wrap:follow" {
		t.Fatalf("agentPreviewWindow(0) = %q", got)
	}
}

func TestTrimTrailingBlankLinesDropsOnlyTrailingBlankLines(t *testing.T) {
	if got, want := trimTrailingBlankLines("one\n\ntwo\n\n\n"), "one\n\ntwo"; got != want {
		t.Fatalf("trimmed preview = %q, want %q", got, want)
	}
}

func TestParseTerminalRows(t *testing.T) {
	if got := parseTerminalRows("40 100\n"); got != 40 {
		t.Fatalf("parseTerminalRows() = %d, want 40", got)
	}
	for _, value := range []string{"", "bad", "0 100", "40"} {
		if got := parseTerminalRows(value); got != 0 {
			t.Fatalf("parseTerminalRows(%q) = %d, want 0", value, got)
		}
	}
}

func TestCodexStatusFromPaneTitle(t *testing.T) {
	cases := map[string]agentStatus{
		"Ready":                                      agentStatusWaiting,
		"Working":                                    agentStatusWorking,
		"\u2866 Working":                             agentStatusWorking,
		"Thinking":                                   agentStatusWorking,
		"Action Required":                            agentStatusAttention,
		"[ . ] Action Required":                      agentStatusAttention,
		"tmux-menu|Ready":                            agentStatusWaiting,
		"tmux-menu|Working":                          agentStatusWorking,
		"tmux-menu|Thinking":                         agentStatusWorking,
		"tmux-menu|\u2866 Working":                   agentStatusWorking,
		"tmux-menu|[ . ] Action Required":            agentStatusAttention,
		"tmux-menu|add agents title|Ready":           agentStatusWaiting,
		"tmux-menu|add agents title |Working":        agentStatusWorking,
		"tmux-menu|add agents title|Action Required": agentStatusAttention,
		"tmux-menu|add agents title|[ . ] Action Required":      agentStatusAttention,
		"\u283c 019ea1db-72dc-79c2-8a11-72f1559360a0 | Working": agentStatusWorking,
		".| 019ea1e9-dae6-7551-8005-24a2cb6a95be":               agentStatusWorking,
		"|019ea1db-72dc-79c2-8a11-72f1559360a0":                 agentStatusWaiting,
	}
	for title, want := range cases {
		got, ok := codexStatusFromPaneTitle(title)
		if !ok {
			t.Fatalf("codexStatusFromPaneTitle(%q) did not find status", title)
		}
		if got != want {
			t.Fatalf("codexStatusFromPaneTitle(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestCodexPaneTitleStatusDoesNotRequireCodexCommand(t *testing.T) {
	pane := tmux.Pane{
		PanePID:        "1234",
		CurrentCommand: "node",
		PaneTitle:      "\u2826 019ea1db-72dc-79c2-8a11-72f1559360a0 | Working",
	}
	got := agentPaneStatus(pane, processSnapshot{
		statuses: map[int]agentStatus{1234: agentStatusWaiting},
	})
	if got != agentStatusWorking {
		t.Fatalf("agentPaneStatus() = %q, want working from Codex pane title", got)
	}
}

func TestCodexWindowNameStatusFallback(t *testing.T) {
	pane := tmux.Pane{
		PanePID:        "1234",
		CurrentCommand: "node",
		PaneTitle:      "019ea1db-72dc-79c2-8a11-72f1559360a0",
		WindowName:     "\u2839 019ea1db-72dc-79c2-8a11-72f1559360a0 | Working",
	}
	got := agentPaneStatus(pane, processSnapshot{
		statuses: map[int]agentStatus{1234: agentStatusWaiting},
	})
	if got != agentStatusWorking {
		t.Fatalf("agentPaneStatus() = %q, want working from Codex window name", got)
	}
}

func TestClaudeStatusFromPaneTitle(t *testing.T) {
	cases := map[string]agentStatus{
		"\u2733 Scribe - keyless":         agentStatusWaiting,
		"\u273b Scribe - keyless":         agentStatusWaiting,
		"\u2722 loop run":                 agentStatusWaiting,
		"\u273d Claude Code":              agentStatusWorking,
		"\u2807 Claude Code":              agentStatusWorking,
		"\u00b7|\u00b7 Claude Code":       agentStatusWorking,
		"\u2733 needs input: pick target": agentStatusAttention,
		"Claude Code permission required": agentStatusAttention,
	}
	for title, want := range cases {
		got, ok := claudeStatusFromPaneTitle(title)
		if !ok {
			t.Fatalf("claudeStatusFromPaneTitle(%q) did not find status", title)
		}
		if got != want {
			t.Fatalf("claudeStatusFromPaneTitle(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestClaudePaneTitleStatusOverridesProcessStatus(t *testing.T) {
	pane := tmux.Pane{PanePID: "1234", CurrentCommand: "2.1.165", PaneTitle: "\u00b7|\u00b7 Claude Code"}
	got := agentPaneStatus(pane, processSnapshot{
		statuses: map[int]agentStatus{1234: agentStatusWaiting},
	})
	if got != agentStatusWorking {
		t.Fatalf("agentPaneStatus() = %q, want working from Claude pane title", got)
	}
}

func TestManualWindowNamesDoNotChangeAgentRowsOrTitleStatuses(t *testing.T) {
	codex := tmux.Pane{
		SessionName: "work", WindowName: "manual-codex", WindowIndex: "1", PaneIndex: "1",
		PaneID: "%1", PanePID: "100", PaneTitle: "tmux-menu|Working", CurrentCommand: "codex",
	}
	claude := tmux.Pane{
		SessionName: "work", WindowName: "manual-claude", WindowIndex: "2", PaneIndex: "1",
		PaneID: "%2", PanePID: "200", PaneTitle: "\u2733 Claude Code", CurrentCommand: "claude",
	}
	snapshot := processSnapshot{
		roots: map[int]bool{100: true, 200: true},
		statuses: map[int]agentStatus{
			100: agentStatusWaiting,
			200: agentStatusWorking,
		},
		names: map[int]string{100: "codex", 200: "claude"},
	}
	items := agentItemsWithProcessSnapshot([]tmux.Pane{codex, claude}, "", snapshot)

	if got := stripANSI(items[0].Label); strings.Contains(got, "manual-codex") || !strings.HasPrefix(got, "● >") {
		t.Fatalf("Codex agent row or title-derived status changed: %q", got)
	}
	if got := stripANSI(items[1].Label); strings.Contains(got, "manual-claude") || !strings.HasPrefix(got, "○ ✳") {
		t.Fatalf("Claude agent row or title-derived status changed: %q", got)
	}
}

func TestSingleClaudeDotTitleDoesNotClaimWorking(t *testing.T) {
	if got, ok := claudeStatusFromPaneTitle("\u2219 Claude Code"); ok {
		t.Fatalf("single dot title should not be treated as working, got %q", got)
	}
}

func TestAgentPaneLabelUsesCodexThreadTitle(t *testing.T) {
	label := stripANSI(agentPaneLabel(tmux.Pane{
		SessionName:    "work",
		WindowIndex:    "1",
		PaneIndex:      "2",
		PaneID:         "%2",
		CurrentCommand: "codex",
		PaneTitle:      "tmux-menu|add agents title |Working",
		CurrentPath:    "/tmp/project",
	}, "", agentStatusWorking, "codex", config.DefaultSessionColor))

	if !strings.Contains(label, "add agents title") {
		t.Fatalf("label should include thread title: %q", label)
	}
	if strings.Contains(label, "tmux-menu|") || strings.Contains(label, "|Working") {
		t.Fatalf("label should not include raw pipe title: %q", label)
	}
}

func TestAgentPaneLabelCompactsCodexSpinnerThreadTitle(t *testing.T) {
	label := stripANSI(agentPaneLabel(tmux.Pane{
		SessionName:    "work",
		WindowIndex:    "1",
		PaneIndex:      "2",
		PaneID:         "%2",
		CurrentCommand: "node",
		PaneTitle:      "\u2826 019ea1db-72dc-79c2-8a11-72f1559360a0 | Working",
		CurrentPath:    "/tmp/project",
	}, "", agentStatusWorking, "codex", config.DefaultSessionColor))

	if !strings.Contains(label, "019ea1db") {
		t.Fatalf("label should include compact Codex thread title: %q", label)
	}
	if strings.Contains(label, "\u2826") || strings.Contains(label, "| Working") || strings.Contains(label, "72dc-79c2") {
		t.Fatalf("label should not include raw spinner/status title: %q", label)
	}
}

func TestAgentPaneLabelCompactsCodexCurrentDirUUID(t *testing.T) {
	label := stripANSI(agentPaneLabel(tmux.Pane{
		SessionName:    "denti_ai",
		WindowIndex:    "4",
		PaneIndex:      "1",
		PaneID:         "%63",
		CurrentCommand: "node",
		PaneTitle:      "019fb87c-abf4-7832-81c9-82e8e8865e64 | Ready",
		CurrentPath:    "/home/alice/.dotfiles",
	}, "", agentStatusWaiting, "codex", config.DefaultSessionColor))

	if !strings.Contains(label, "019fb87c") || strings.Contains(label, "abf4-7832") {
		t.Fatalf("label should include compact Codex current-dir UUID: %q", label)
	}
}

func TestAgentPaneLabelDoesNotUseCodexStateAsTitle(t *testing.T) {
	label := stripANSI(agentPaneLabel(tmux.Pane{
		SessionName:    "work",
		WindowIndex:    "1",
		PaneIndex:      "2",
		PaneID:         "%2",
		CurrentCommand: "codex-aarch64-a",
		PaneTitle:      "Ready",
		CurrentPath:    "/home/alice/projects/tmux-menu",
	}, "", agentStatusWaiting, "codex", config.DefaultSessionColor))

	if strings.Contains(label, "Ready") {
		t.Fatalf("label should not show Codex state as row title: %q", label)
	}
	if !strings.Contains(label, "tmux-menu") {
		t.Fatalf("label should fall back to current dir name: %q", label)
	}
}

func TestCodexPaneTitleStatusOverridesProcessStatus(t *testing.T) {
	pane := tmux.Pane{PanePID: "1234", CurrentCommand: "codex", PaneTitle: "tmux-menu|Ready"}
	got := agentPaneStatus(pane, processSnapshot{
		statuses: map[int]agentStatus{1234: agentStatusWorking},
	})
	if got != agentStatusWaiting {
		t.Fatalf("agentPaneStatus() = %q, want waiting from pane title", got)
	}
}

func TestColorAgentStatus(t *testing.T) {
	cases := map[agentStatus]string{
		agentStatusAttention: "!",
		agentStatusWorking:   "●",
		agentStatusCompleted: "✓",
		agentStatusWaiting:   "○",
		agentStatusUnknown:   "?",
	}
	for status, want := range cases {
		if got := stripANSI(colorAgentStatus(status)); got != want {
			t.Fatalf("colorAgentStatus(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestColorAgentStatusHighlightsAttention(t *testing.T) {
	got := colorAgentStatus(agentStatusAttention)
	if !strings.Contains(got, ansiRed) || !strings.Contains(got, ansiBold) {
		t.Fatalf("attention status should be bold red, got %q", got)
	}
}

func TestColorAgentStatusUsesStateColors(t *testing.T) {
	cases := map[agentStatus]string{
		agentStatusWorking:   ansiGreen,
		agentStatusCompleted: ansiBrightCyan,
		agentStatusWaiting:   ansiYellow,
		agentStatusUnknown:   ansiDim,
	}
	for status, want := range cases {
		if got := colorAgentStatus(status); !strings.Contains(got, want) {
			t.Fatalf("colorAgentStatus(%q) = %q, want color %q", status, got, want)
		}
	}
}

func TestAgentRowsShowConfiguredIconBeforeSessionAndHideForegroundCommand(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", WindowIndex: "1", PaneIndex: "1", PaneID: "%1", PanePID: "100", CurrentCommand: "node", PaneTitle: "|thread", CurrentPath: "/tmp/project"},
		{SessionName: "work", WindowIndex: "2", PaneIndex: "1", PaneID: "%2", PanePID: "200", CurrentCommand: "2.1.217", PaneTitle: "\u00b7|\u00b7 Claude Code", CurrentPath: "/tmp/project"},
	}
	items := agentItemsWithProcessSnapshot(panes, "", processSnapshot{
		roots:    map[int]bool{100: true, 200: true},
		statuses: map[int]agentStatus{100: agentStatusWaiting, 200: agentStatusWorking},
		names:    map[int]string{100: "codex", 200: "claude"},
	})

	if len(items) != 2 {
		t.Fatalf("expected two agent rows, got %d", len(items))
	}
	codex := stripANSI(items[0].Label)
	claude := stripANSI(items[1].Label)
	if !strings.HasPrefix(codex, "○ > work") || strings.Contains(codex, "work/1.1") || strings.Contains(codex, "node") {
		t.Fatalf("unexpected Codex row: %q", codex)
	}
	if !strings.HasPrefix(claude, "● ✳ work") || strings.Contains(claude, "work/2.1") || strings.Contains(claude, "2.1.217") || strings.Count(claude, "✳") != 1 {
		t.Fatalf("unexpected Claude row: %q", claude)
	}
	if !strings.Contains(items[0].Label, ansiBlue) {
		t.Fatalf("Codex icon should be blue: %q", items[0].Label)
	}
	if !strings.Contains(items[1].Label, ansiOrange) {
		t.Fatalf("Claude icon should be orange: %q", items[1].Label)
	}
}

func TestAgentRowCompactsUUIDSessionAndMarksCurrentAgent(t *testing.T) {
	label := stripANSI(agentPaneLabel(tmux.Pane{
		SessionName: "019fbdaa-d18a-7a61-9c2e-4f6e5863c787",
		WindowIndex: "4",
		PaneIndex:   "1",
		PaneID:      "%1",
		PaneTitle:   "Ready",
		CurrentPath: "/tmp/project",
	}, "%1", agentStatusWaiting, "codex", config.DefaultSessionColor))

	if !strings.HasPrefix(label, "○ > 019fbdaa  *project") {
		t.Fatalf("unexpected compact current-agent row: %q", label)
	}
	if strings.Contains(label, "current") || strings.Contains(label, "d18a-7a61") || strings.Contains(label, "/4.1") {
		t.Fatalf("row should omit the old marker and UUID tail: %q", label)
	}
}

func TestAgentRowUsesConfiguredSessionColor(t *testing.T) {
	pane := tmux.Pane{
		SessionName: "clos", SessionID: "$1", WindowIndex: "3", PaneIndex: "1",
		PaneID: "%1", PanePID: "100", CurrentCommand: "codex", PaneTitle: "Ready",
	}
	items := agentItemsWithProcessSnapshotAndSessionColors([]tmux.Pane{pane}, "", processSnapshot{}, map[string]string{"$1": "red"})

	if got := stripANSI(items[0].Label); !strings.HasPrefix(got, "○ > clos") || strings.Contains(got, "clos/3.1") {
		t.Fatalf("unexpected agent row: %q", got)
	}
	if !strings.Contains(items[0].Label, ansiBold+ansiRed+"clos") {
		t.Fatalf("session name should be bold red: %q", items[0].Label)
	}
}

func TestANSIColorSupportsAllConfiguredSessionColors(t *testing.T) {
	want := map[string]string{
		"default":        ansiDefault,
		"black":          ansiBlack,
		"red":            ansiRed,
		"green":          ansiGreen,
		"yellow":         ansiYellow,
		"blue":           ansiBlue,
		"magenta":        ansiMagenta,
		"cyan":           ansiCyan,
		"white":          ansiWhite,
		"bright_black":   ansiBrightBlack,
		"bright_red":     ansiBrightRed,
		"bright_green":   ansiBrightGreen,
		"bright_yellow":  ansiBrightYellow,
		"bright_blue":    ansiBrightBlue,
		"bright_magenta": ansiBrightMagenta,
		"bright_cyan":    ansiBrightCyan,
		"bright_white":   ansiBrightWhite,
		"orange":         ansiOrange,
	}
	for color, code := range want {
		if got := ansiColor(color); got != code {
			t.Fatalf("ansiColor(%q) = %q, want %q", color, got, code)
		}
	}
}

func TestLoadAgentSessionColorsUsesEachSessionRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	roots := make(map[string]string)
	for _, session := range []struct {
		name  string
		color string
	}{
		{name: "clos", color: "red"},
		{name: "denti_ai", color: "green"},
	} {
		root := filepath.Join(t.TempDir(), session.name)
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatal(err)
		}
		contents := fmt.Sprintf("[session]\ncolor = %q\n", session.color)
		if err := os.WriteFile(filepath.Join(root, ".tmux-menu.conf"), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
		roots[session.name] = root
	}

	panes := []tmux.Pane{
		{SessionName: "clos", SessionID: "$1", SessionPath: roots["clos"]},
		{SessionName: "denti_ai", SessionID: "$2", SessionPath: roots["denti_ai"]},
	}
	colors, err := loadAgentSessionColors(panes, config.DefaultSessionColor)
	if err != nil {
		t.Fatal(err)
	}
	if colors["$1"] != "red" || colors["$2"] != "green" {
		t.Fatalf("session colors = %#v", colors)
	}
}

func TestShortUUIDLeavesNonUUIDNamesUnchanged(t *testing.T) {
	for _, name := range []string{"work", "denti_ai", "019fbdaa-not-a-uuid"} {
		if got := shortUUID(name); got != name {
			t.Fatalf("shortUUID(%q) = %q", name, got)
		}
	}
}

func TestToolsShowsCommandsAndQuickDirs(t *testing.T) {
	items := toolsItemsForTest()
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
	}
	if len(labels) != 2 {
		t.Fatalf("expected command and dir labels only: %#v", labels)
	}
	if !strings.HasPrefix(labels[0], "cmd") ||
		!strings.HasPrefix(labels[1], "dir") {
		t.Fatalf("expected commands then quick dirs:\n%s", strings.Join(labels, "\n"))
	}
}

func TestAgentPanesUsesProcessTree(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", WindowIndex: "1", PaneIndex: "1", PaneID: "%1", PanePID: "1482", CurrentCommand: "2.1.142", PaneTitle: "* activation ci"},
		{SessionName: "work", SessionID: "$1", WindowIndex: "1", PaneIndex: "2", PaneID: "%2", PanePID: "2000", CurrentCommand: "zsh"},
	}
	agents := agentPanesWithProcessSnapshot(panes, processSnapshot{roots: map[int]bool{1482: true}})
	if len(agents) != 1 {
		t.Fatalf("expected one process-tree agent pane: %#v", agents)
	}
	if agents[0].PaneID != "%1" {
		t.Fatalf("wrong agent pane: %#v", agents[0])
	}
}

func TestQuickDirsResolveRelativeToSessionRoot(t *testing.T) {
	sessionRoot := t.TempDir()
	cfg := config.Default()
	cfg.QuickDirs = []config.QuickDir{
		{Title: "docs", Path: "./docs", Session: "app"},
		{Title: "ops docs", Path: "./docs", Session: "ops"},
	}
	items := toolsItems(cfg, "/tmp/current-pane", "app", sessionRoot)
	if len(items) != 1 {
		t.Fatalf("expected only app quick dir: %#v", items)
	}
	got := items[0].Value.dispatch.Cmd
	want := "cd " + shellquote.Quote(filepath.Join(sessionRoot, "docs"))
	if got != want {
		t.Fatalf("quick dir command = %q, want %q", got, want)
	}
}

func TestQuickDirCommandRunsAfterCd(t *testing.T) {
	sessionRoot := t.TempDir()
	cfg := config.Default()
	cfg.QuickDirs = []config.QuickDir{
		{Title: "admin", Path: "./services/admin", Command: "git status"},
	}

	items := toolsItems(cfg, "/tmp/current-pane", "app", sessionRoot)
	if len(items) != 1 {
		t.Fatalf("expected one quick dir: %#v", items)
	}
	got := items[0].Value.dispatch.Cmd
	want := "cd " + shellquote.Quote(filepath.Join(sessionRoot, "services", "admin")) + " && git status"
	if got != want {
		t.Fatalf("quick dir command = %q, want %q", got, want)
	}
	if label := stripANSI(items[0].Label); !strings.Contains(label, "[git status]") {
		t.Fatalf("quick dir label should show command, got %q", label)
	}
}

func TestToolsFiltersCommandsBySession(t *testing.T) {
	cfg := config.Default()
	cfg.Commands = []config.Command{
		{Title: "Global", Mode: "paste", Cmd: "echo global"},
		{Title: "Work only", Mode: "paste", Cmd: "echo work", Session: "work"},
		{Title: "Other only", Mode: "paste", Cmd: "echo other", Session: "other"},
	}

	items := toolsItems(cfg, "", "work", "")
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
	}
	if len(labels) != 2 {
		t.Fatalf("expected global and work command only: %#v", labels)
	}
	if !strings.Contains(labels[0], "Global") || !strings.Contains(labels[1], "Work only") {
		t.Fatalf("wrong command labels: %#v", labels)
	}
}

func TestToolsIncludesMakefileTargetsFromSessionAndOrigin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionRoot := filepath.Join(home, "session")
	originPath := filepath.Join(home, "projects", "work")
	if err := os.MkdirAll(sessionRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(originPath, 0755); err != nil {
		t.Fatal(err)
	}
	sessionMakefile := `.PHONY: test
SHELL := /bin/bash
CLOUD_PROFILE ?= dev
BACKEND_RECONFIGURE_ARG := -reconfigure
TFVARS += shared.tfvars
FROM_SHELL != date
PLAIN = value
build test:
	@echo shadow:
`
	if err := os.WriteFile(filepath.Join(sessionRoot, "Makefile"), []byte(sessionMakefile), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(originPath, "Makefile"), []byte("lint:\n\t@true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	items := toolsItems(cfg, originPath, "work", sessionRoot)
	labels := make([]string, 0, len(items))
	commands := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
		commands = append(commands, item.Value.dispatch.Cmd)
	}

	for _, want := range []string{"make", "build", "test", "lint"} {
		if !strings.Contains(strings.Join(labels, "\n"), want) {
			t.Fatalf("make target labels missing %q:\n%s", want, strings.Join(labels, "\n"))
		}
	}
	if !containsString(commands, `make -C "$HOME/session" build`) ||
		!containsString(commands, `make -C "$HOME/session" test`) ||
		!containsString(commands, `make -C "$HOME/projects/work" lint`) {
		t.Fatalf("wrong make commands: %#v", commands)
	}
	joinedLabels := strings.Join(labels, "\n")
	for _, unexpected := range []string{
		"shadow",
		"SHELL",
		"CLOUD_PROFILE",
		"BACKEND_RECONFIGURE_ARG",
		"TFVARS",
		"FROM_SHELL",
		"PLAIN",
	} {
		if strings.Contains(joinedLabels, unexpected) {
			t.Fatalf("non-target %q was parsed as a make target:\n%s", unexpected, joinedLabels)
		}
	}
}

func TestStatusItemsListFilesBySubdirAndOpenEditorInPane(t *testing.T) {
	t.Setenv("EDITOR", "nvim")
	sessionRoot := t.TempDir()
	todoDir := filepath.Join(sessionRoot, "todo")
	for _, dir := range []string{
		filepath.Join(todoDir, "new"),
		filepath.Join(todoDir, "backlog"),
		filepath.Join(todoDir, "doing"),
		filepath.Join(todoDir, "done"),
		filepath.Join(todoDir, "old"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{
		filepath.Join(todoDir, "inbox.md"),
		filepath.Join(todoDir, "new", "default-new.md"),
		filepath.Join(todoDir, "backlog", "plan_status-board.md"),
		filepath.Join(todoDir, "doing", "ship-status.md"),
		filepath.Join(todoDir, "done", "done-task.md"),
		filepath.Join(todoDir, "old", "archived-task.md"),
		filepath.Join(todoDir, "done", ".gitkeep"),
	}
	contents := map[string]string{
		filepath.Join(todoDir, "backlog", "plan_status-board.md"): "# Goal\nsummary: Show backlog before active work.\n\nFull body.\n",
		filepath.Join(todoDir, "doing", "ship-status.md"):         "# Ship status board\nsummary: Replace flat status list with a board.\n",
		filepath.Join(todoDir, "done", "done-task.md"):            "# Done task\nsummary: Keep completed work visible.\n",
	}
	for _, file := range files {
		body := contents[file]
		if body == "" {
			body = "# task\nsummary: should not appear\n"
		}
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.Status.StatusDirs = []string{"./todo"}
	cfg.Status.Statuses = []string{"backlog", "doing", "done"}
	cfg.Status.Open = config.OpenConfig{Mode: "pane", PaneSide: "left"}
	items, err := statusItems(cfg.Status, cfg.Editor, sessionRoot)
	if err != nil {
		t.Fatal(err)
	}

	labels := make([]string, 0, len(items))
	commands := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
		commands = append(commands, item.Value.dispatch.Cmd)
		if item.Value.dispatch.Mode != "pane" {
			t.Fatalf("status selection should open a new pane, got %#v", item.Value.dispatch)
		}
		if item.Value.dispatch.PaneSide != "left" {
			t.Fatalf("status editor pane side = %q, want left", item.Value.dispatch.PaneSide)
		}
		if item.Value.dispatch.WorkingDir != sessionRoot {
			t.Fatalf("status editor pane dir = %q, want session root", item.Value.dispatch.WorkingDir)
		}
	}

	gotLabels := strings.Join(labels, "\n")
	for _, want := range []string{
		"BACKLOG",
		"plan status board",
		"Show backlog before active work.",
		"DOING",
		"ship status",
		"Replace flat status list with a board.",
		"DONE",
		"done task",
		"Keep completed work visible.",
	} {
		if !strings.Contains(gotLabels, want) {
			t.Fatalf("status labels missing %q:\n%s", want, gotLabels)
		}
	}
	for _, unexpected := range []string{"Goal", "Ship status board", "Done task", "inbox.md", "default-new.md", "archived-task.md", "should not appear"} {
		if strings.Contains(gotLabels, unexpected) {
			t.Fatalf("status labels should only show configured status dirs, found %q:\n%s", unexpected, gotLabels)
		}
	}
	for _, file := range files {
		if strings.Contains(gotLabels, file) || strings.Contains(gotLabels, shortenHome(file)) {
			t.Fatalf("status labels should not show file paths, found %q in:\n%s", file, gotLabels)
		}
	}
	for _, label := range labels {
		if strings.HasPrefix(label, "status ") {
			t.Fatalf("status label should omit redundant kind: %q", label)
		}
	}
	if strings.Contains(gotLabels, ".gitkeep") {
		t.Fatalf("status labels should omit redundant kind and ignored files:\n%s", gotLabels)
	}
	for _, item := range items {
		if strings.HasPrefix(item.Preview, "'") || strings.HasSuffix(item.Preview, "'") {
			t.Fatalf("preview path should be raw for fzf placeholder quoting, got %q", item.Preview)
		}
	}
	for _, file := range files {
		if strings.HasSuffix(file, ".gitkeep") || strings.Contains(file, "/old/") || strings.Contains(file, "/new/") ||
			strings.HasSuffix(file, "inbox.md") {
			continue
		}
		if !containsCommandWith(commands, "nvim", file) {
			t.Fatalf("status editor command missing %q in %#v", file, commands)
		}
	}
}

func TestStatusFooterShowsBoardHelpAndNavigation(t *testing.T) {
	cfg := config.Default()
	cfg.Status.Statuses = []string{"backlog", "doing", "done"}

	got := statusFooter(cfg)
	want := "BACKLOG / DOING / DONE | Space preview | Enter edit | Ctrl-C cancel\n" + viewSwitchHelp
	if got != want {
		t.Fatalf("statusFooter() = %q, want %q", got, want)
	}
}

func TestProjectDispatchUsesNativeProjectMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux-menu")
	d := projectDispatch(path, ".tmux-bootstrap")
	if d.Mode != "project" {
		t.Fatalf("unexpected mode: %q", d.Mode)
	}
	if d.ProjectPath != path {
		t.Fatalf("project path = %q, want %q", d.ProjectPath, path)
	}
	if d.BootstrapFile != ".tmux-bootstrap" {
		t.Fatalf("bootstrap file = %q", d.BootstrapFile)
	}
	if strings.Contains(d.Cmd, "tmux-sessionizer") {
		t.Fatalf("project dispatch should not depend on script: %q", d.Cmd)
	}
}

func TestListProjectsUsesConfiguredRoots(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	for _, path := range []string{
		filepath.Join(rootA, "alpha"),
		filepath.Join(rootB, "beta"),
	} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(rootA, "README.md"), []byte("skip\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := listProjectsInRoots([]string{rootB, "/tmp/missing-tmux-menu-root", rootA})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(rootA, "alpha"), filepath.Join(rootB, "beta")}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("projects = %#v, want %#v", got, want)
	}
}

func TestProjectLabelShowsBootstrapMarker(t *testing.T) {
	project := t.TempDir()
	plain := stripANSI(projectLabel(project, ".tmux-sessionizer"))
	if !strings.Contains(plain, "no bootstrap") {
		t.Fatalf("missing no-bootstrap marker: %q", plain)
	}
	if err := os.WriteFile(filepath.Join(project, ".tmux-sessionizer"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	bootstrapped := stripANSI(projectLabel(project, ".tmux-sessionizer"))
	if !strings.Contains(bootstrapped, "bootstrap") || strings.Contains(bootstrapped, "no bootstrap") {
		t.Fatalf("missing bootstrap marker: %q", bootstrapped)
	}
}

func TestProjectLabelUsesConfiguredBootstrapFile(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".project-tmux"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	label := stripANSI(projectLabel(project, ".project-tmux"))
	if !strings.Contains(label, "bootstrap") || strings.Contains(label, "no bootstrap") {
		t.Fatalf("custom bootstrap file was not detected: %q", label)
	}
}

func TestProjectItemsUseStableUniqueSessionNamesForDuplicateBasenames(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "api")
	rootB := filepath.Join(t.TempDir(), "api")
	for _, dir := range []string{rootA, rootB} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	items := projectItems([]string{rootA, rootB}, ".tmux-sessionizer")
	if len(items) != 2 {
		t.Fatalf("project items = %#v", items)
	}
	first := items[0].Value.dispatch.ProjectSessionName
	second := items[1].Value.dispatch.ProjectSessionName
	if first == "" || second == "" || first == second {
		t.Fatalf("duplicate basenames should get unique session names: %q %q", first, second)
	}
	if first == "api" || second == "api" {
		t.Fatalf("colliding project session names should not stay as bare basename: %q %q", first, second)
	}
	labels := stripANSI(items[0].Label + "\n" + items[1].Label)
	if !strings.Contains(labels, first) || !strings.Contains(labels, second) {
		t.Fatalf("duplicate session names should be visible in labels:\n%s", labels)
	}
}

func TestLoadRuntimeContextFailsOnTmuxDisplayError(t *testing.T) {
	restore := stubDisplayTmux(t, func(ctx context.Context, format string) (string, error) {
		return "", fmt.Errorf("no tmux")
	})
	defer restore()

	_, err := loadRuntimeContext(context.Background())
	if err == nil {
		t.Fatal("expected tmux context error")
	}
	if !strings.Contains(err.Error(), "#{pane_id}") {
		t.Fatalf("context error should name failing tmux format, got: %v", err)
	}
}

func TestLoadRuntimeContextUsesPopupEnvironment(t *testing.T) {
	t.Setenv("TMUX_MENU_ORIGIN_PANE", "%7")
	t.Setenv("TMUX_MENU_ORIGIN_PATH", "/tmp/project")
	t.Setenv("TMUX_MENU_SESSION_ID", "$1")
	t.Setenv("TMUX_MENU_SESSION_NAME", "work")
	t.Setenv("TMUX_MENU_SESSION_PATH", "/tmp")
	restore := stubDisplayTmux(t, func(ctx context.Context, format string) (string, error) {
		t.Fatalf("display should not be called for complete popup environment: %s", format)
		return "", nil
	})
	defer restore()

	got, err := loadRuntimeContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginPane != "%7" || got.SessionID != "$1" || got.SessionName != "work" {
		t.Fatalf("runtime context = %#v", got)
	}
}

func TestLinkDispatchOpensFileInEditorPopup(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	d := linkDispatch(linkscan.Item{
		Kind:   linkscan.KindFile,
		Target: "/tmp/project/main.go",
		Line:   12,
		Column: 3,
	}, "/tmp/project", config.Default().Editor, config.Default().Links.Open)
	if d.Mode != "sequence" {
		t.Fatalf("mode = %q, want sequence", d.Mode)
	}
	if len(d.Steps) != 2 {
		t.Fatalf("steps = %#v, want copy and open", d.Steps)
	}
	if d.Steps[0].Mode != "shell" || d.Steps[0].Cmd != "printf %s '/tmp/project/main.go' | pbcopy" {
		t.Fatalf("copy step = %#v", d.Steps[0])
	}
	open := d.Steps[1]
	if open.Mode != "popup" {
		t.Fatalf("mode = %q, want popup", open.Mode)
	}
	if open.WorkingDir != "/tmp/project" {
		t.Fatalf("working dir = %q, want origin path", open.WorkingDir)
	}
	if open.PopupWidth != "80%" || open.PopupHeight != "80%" || open.PopupBorder != "rounded" {
		t.Fatalf("unexpected editor popup size/border: %#v", open)
	}
	if !strings.Contains(open.Cmd, "vim") ||
		!strings.Contains(open.Cmd, "+call cursor(12,3)") ||
		!strings.Contains(open.Cmd, "/tmp/project/main.go") {
		t.Fatalf("unexpected editor command: %q", open.Cmd)
	}
}

func TestLinkDispatchCanOpenFileInLeftPane(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	openConfig := config.OpenConfig{Mode: "pane", PaneSide: "left"}
	d := linkDispatch(linkscan.Item{
		Kind:   linkscan.KindFile,
		Target: "/tmp/project/main.go",
		Line:   12,
	}, "/tmp/project", config.Default().Editor, openConfig)
	if d.Mode != "sequence" || len(d.Steps) != 2 {
		t.Fatalf("dispatch = %#v, want copy plus pane open", d)
	}
	open := d.Steps[1]
	if open.Mode != "pane" || open.PaneSide != "left" || open.WorkingDir != "/tmp/project" {
		t.Fatalf("open step = %#v, want left pane", open)
	}
	if !strings.Contains(open.Cmd, "vim") || !strings.Contains(open.Cmd, "+12") {
		t.Fatalf("open command = %q", open.Cmd)
	}
}

func TestLinkDispatchCanOpenFileInWindow(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	openConfig := config.OpenConfig{Mode: "window", PaneSide: "right"}
	d := linkDispatch(linkscan.Item{
		Kind:   linkscan.KindFile,
		Target: "/tmp/project/main.go",
	}, "/tmp/project", config.Default().Editor, openConfig)
	open := d.Steps[1]
	if open.Mode != "window" || open.WorkingDir != "/tmp/project" {
		t.Fatalf("open step = %#v, want window", open)
	}
}

func TestLinkAlternateDispatchOpensFileWithGUICommand(t *testing.T) {
	alt := config.LinkAlternateConfig{
		Key:         "alt-enter",
		FileCommand: "open -a TextEdit",
		URLCommand:  "open -a Safari",
	}
	d := linkAlternateDispatch(linkscan.Item{
		Kind:   linkscan.KindFile,
		Target: "/tmp/project/read me.md",
		Line:   12,
		Column: 3,
	}, alt)
	if d.Mode != "sequence" {
		t.Fatalf("mode = %q, want sequence", d.Mode)
	}
	if len(d.Steps) != 2 {
		t.Fatalf("steps = %#v, want copy and GUI open", d.Steps)
	}
	if d.Steps[0].Mode != "shell" || d.Steps[0].Cmd != "printf %s '/tmp/project/read me.md' | pbcopy" {
		t.Fatalf("copy step = %#v", d.Steps[0])
	}
	if d.Steps[1].Mode != "shell" || d.Steps[1].Cmd != "open -a TextEdit '/tmp/project/read me.md'" {
		t.Fatalf("GUI open step = %#v", d.Steps[1])
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsCommandWith(commands []string, parts ...string) bool {
	for _, command := range commands {
		found := true
		for _, part := range parts {
			if !strings.Contains(command, part) {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}

func mustWriteText(t *testing.T, path string, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLinkDispatchOpensURLWithDefaultBrowser(t *testing.T) {
	d := linkDispatch(linkscan.Item{Kind: linkscan.KindURL, Target: "https://example.com"}, "/tmp/project", config.Default().Editor, config.Default().Links.Open)
	if d.Mode != "sequence" {
		t.Fatalf("mode = %q, want sequence", d.Mode)
	}
	if len(d.Steps) != 2 {
		t.Fatalf("steps = %#v, want copy and open", d.Steps)
	}
	if d.Steps[0].Mode != "shell" || d.Steps[0].Cmd != "printf %s 'https://example.com' | pbcopy" {
		t.Fatalf("copy step = %#v", d.Steps[0])
	}
	if d.Steps[1].Mode != "shell" || d.Steps[1].Cmd != "open 'https://example.com'" {
		t.Fatalf("url open step = %#v", d.Steps[1])
	}
}

func TestLinkAlternateDispatchOpensURLWithConfiguredApp(t *testing.T) {
	alt := config.LinkAlternateConfig{
		Key:         "alt-enter",
		FileCommand: "open -a TextEdit",
		URLCommand:  "open -a Firefox",
	}
	d := linkAlternateDispatch(linkscan.Item{Kind: linkscan.KindURL, Target: "https://example.com/a b"}, alt)
	if d.Mode != "sequence" {
		t.Fatalf("mode = %q, want sequence", d.Mode)
	}
	if len(d.Steps) != 2 {
		t.Fatalf("steps = %#v, want copy and alternate URL open", d.Steps)
	}
	if d.Steps[0].Mode != "shell" || d.Steps[0].Cmd != "printf %s 'https://example.com/a b' | pbcopy" {
		t.Fatalf("copy step = %#v", d.Steps[0])
	}
	if d.Steps[1].Mode != "shell" || d.Steps[1].Cmd != "open -a Firefox 'https://example.com/a b'" {
		t.Fatalf("alternate URL open step = %#v", d.Steps[1])
	}
}

func TestLinkExpectKeysIncludesCustomAlternateKey(t *testing.T) {
	keys := linkExpectKeys(config.LinkAlternateConfig{Key: "ctrl-o"})
	if !containsLinkExpectKey(keys, "ctrl-o") {
		t.Fatalf("keys = %#v, want ctrl-o", keys)
	}
	if got, want := len(keys), len(viewSwitchKeys)+1; got != want {
		t.Fatalf("len(keys) = %d, want %d", got, want)
	}
}

func TestCommandWithTargetAppendsQuotedTarget(t *testing.T) {
	got := commandWithTarget("open -a TextEdit", "/tmp/project/read me.md")
	want := "open -a TextEdit '/tmp/project/read me.md'"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestCommandWithTargetQuotesShellMetacharacters(t *testing.T) {
	target := "https://example.com/$(printf injected)/a'b"
	command := commandWithTarget("printf %s \"{}\"", target)
	out, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != target {
		t.Fatalf("output = %q, want literal target %q", got, target)
	}
}

func TestDispatchForResultUsesAlternateKey(t *testing.T) {
	regular := action.Dispatch{Mode: "shell", Cmd: "regular"}
	alternate := action.Dispatch{Mode: "shell", Cmd: "alternate"}
	got := dispatchForResult(picker.Result[menuItem]{
		Key:      "alt-enter",
		Selected: true,
		Value: menuItem{
			dispatch:          regular,
			alternateKey:      "alt-enter",
			alternateDispatch: alternate,
		},
	})
	if got.Cmd != "alternate" {
		t.Fatalf("dispatch = %#v, want alternate", got)
	}
	got = dispatchForResult(picker.Result[menuItem]{
		Selected: true,
		Value: menuItem{
			dispatch:          regular,
			alternateKey:      "alt-enter",
			alternateDispatch: alternate,
		},
	})
	if got.Cmd != "regular" {
		t.Fatalf("dispatch = %#v, want regular", got)
	}
}

func TestBookmarkItemsUseConfiguredSourcesInOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	notesRoot := filepath.Join(home, "notes")
	projectRoot := filepath.Join(home, "projects", "sample")
	mustWriteText(t, filepath.Join(notesRoot, "a.md"), `raw https://raw.example/nope
[Note](https://note.example)
![Image](https://image.example/nope)
[Local Note](note-file.md#heading)
`)
	mustWriteText(t, filepath.Join(notesRoot, "sub", "nested.md"), `[Nested](https://nested.example)`)
	mustWriteText(t, filepath.Join(projectRoot, "README.md"), `[Project](https://project.example)`)
	mustWriteText(t, filepath.Join(projectRoot, "vendor", "README.md"), `[Vendor](https://vendor.example)`)
	mustWriteText(t, filepath.Join(notesRoot, ".tmp", "scratch.md"), `[Scratch](https://scratch.example)`)

	cfg := config.Default().Bookmarks
	cfg.Dirs = []string{"~/notes", "~/projects/sample"}
	cfg.Open = config.OpenConfig{Mode: "pane", PaneSide: "left"}
	items, err := bookmarkItems(cfg, config.Default().Editor, "sample")
	if err != nil {
		t.Fatal(err)
	}

	labels := make([]string, 0, len(items))
	commands := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, stripANSI(item.Label))
		commands = append(commands, item.Value.dispatch.Cmd)
	}
	joined := strings.Join(labels, "\n")
	if len(items) != 4 {
		t.Fatalf("bookmark items = %d, labels:\n%s", len(items), joined)
	}
	if !strings.Contains(labels[0], "Note") ||
		!strings.Contains(labels[1], "Local Note") ||
		!strings.Contains(labels[2], "Nested") ||
		!strings.Contains(labels[3], "Project") {
		t.Fatalf("expected configured source order:\n%s", joined)
	}
	if !strings.HasPrefix(labels[0], "notes  ") || !strings.HasPrefix(labels[3], "sample  ") {
		t.Fatalf("bookmark labels should start with source dir names:\n%s", joined)
	}
	for _, unexpected := range []string{"bookmark", "raw.example", "image.example", "vendor.example", "scratch.example", home} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("bookmark labels should omit %q:\n%s", unexpected, joined)
		}
	}
	if commands[0] != "open 'https://note.example'" {
		t.Fatalf("url bookmark command = %q", commands[0])
	}
	if items[1].Value.dispatch.Mode != "pane" || items[1].Value.dispatch.PaneSide != "left" {
		t.Fatalf("local bookmark should open in left pane: %#v", items[1].Value.dispatch)
	}
	if !strings.Contains(items[1].Value.dispatch.Cmd, filepath.Join(notesRoot, "note-file.md")) {
		t.Fatalf("local bookmark command = %q", items[1].Value.dispatch.Cmd)
	}
}

func TestBookmarkDispatchOpensLocalFileInRightPane(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	source := "/tmp/project/docs/links.md"
	bookmark := bookmarkItem{
		Text:       "Runbook",
		Target:     "../runbook.md#deploy",
		SourceFile: source,
		Line:       7,
	}

	d := bookmarkDispatch(bookmark, config.Default().Editor, config.OpenConfig{Mode: "popup", PaneSide: "right"})
	if d.Mode != "popup" {
		t.Fatalf("dispatch should open a popup: %#v", d)
	}
	if d.PopupWidth != "80%" || d.PopupHeight != "80%" || d.PopupBorder != "rounded" {
		t.Fatalf("unexpected popup config: %#v", d)
	}
	if d.WorkingDir != "/tmp/project" {
		t.Fatalf("working dir = %q, want resolved file dir", d.WorkingDir)
	}
	if !strings.Contains(d.Cmd, "vim") ||
		!strings.Contains(d.Cmd, "/tmp/project/runbook.md") ||
		strings.Contains(d.Cmd, "#deploy") {
		t.Fatalf("editor command = %q", d.Cmd)
	}
}

func TestRunPopupAcceptsBookmarksMode(t *testing.T) {
	restore := stubDisplayTmux(t, func(ctx context.Context, format string) (string, error) {
		return "", fmt.Errorf("no tmux")
	})
	defer restore()

	err := runPopup(context.Background(), []string{"bookmarks"})
	if err == nil {
		t.Fatal("expected tmux context error")
	}
	if strings.Contains(err.Error(), "popup mode must") {
		t.Fatalf("bookmarks should be an accepted popup mode, got: %v", err)
	}
}

func TestViewModeForKey(t *testing.T) {
	cases := map[string]string{
		"alt-1":  "palette",
		"alt-2":  "agents",
		"alt-3":  "tools",
		"alt-4":  "projects",
		"alt-5":  "status",
		"alt-6":  "bookmarks",
		"ctrl-x": "",
	}
	for key, want := range cases {
		if got := viewModeForKey(key); got != want {
			t.Fatalf("viewModeForKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestTabViewModeUsesConfiguredOrder(t *testing.T) {
	order := []string{"agents", "palette", "links"}
	cases := []struct {
		current string
		key     string
		want    string
	}{
		{"agents", "tab", "palette"},
		{"links", "tab", "agents"},
		{"agents", "btab", "links"},
		{"palette", "btab", "agents"},
		{"status", "tab", "agents"},
		{"status", "btab", "links"},
		{"agents", "ctrl-x", ""},
	}
	for _, tc := range cases {
		if got := tabViewMode(tc.current, tc.key, order); got != tc.want {
			t.Fatalf("tabViewMode(%q, %q) = %q, want %q", tc.current, tc.key, got, tc.want)
		}
	}
}

func TestViewSwitchFooter(t *testing.T) {
	if got := viewSwitchFooter(); got != viewSwitchHelp {
		t.Fatalf("navigation footer = %q, want %q", got, viewSwitchHelp)
	}
}

func TestPickerPreviewWindowUsesConfiguredWidth(t *testing.T) {
	if got := pickerPreviewWindow("45%", "wrap", "follow"); got != "right:45%:wrap:follow" {
		t.Fatalf("pickerPreviewWindow() = %q", got)
	}
}

func TestSessionLabelOmitsWorkDirAndMarksCurrentExplicitly(t *testing.T) {
	panes := []tmux.Pane{
		{SessionName: "work", SessionID: "$1", CurrentPath: "/tmp/should-not-show"},
		{SessionName: "work", SessionID: "$1", CurrentPath: "/tmp/other"},
	}
	got := stripANSI(sessionLabel(panes[0], panes, "$1"))
	if strings.Contains(got, "/tmp/should-not-show") {
		t.Fatalf("session label should not show work dir: %q", got)
	}
	if strings.Contains(got, "*") {
		t.Fatalf("session label should not use ambiguous star marker: %q", got)
	}
	if !strings.Contains(got, "current") {
		t.Fatalf("session label should mark current explicitly: %q", got)
	}
}

func TestCommandClassHighlightsRemoteAndAgentCommands(t *testing.T) {
	cases := map[string]string{
		"codex-aarch64-a": "agent",
		"claude":          "agent",
		"mosh-client":     "remote",
		"ssh":             "remote",
		"zsh":             "",
	}
	for command, want := range cases {
		if got := commandClass(command); got != want {
			t.Fatalf("commandClass(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestProcessCommandClassUsesExecutableNamesNotArguments(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/nvim /home/alice/.config/tool/config.toml": "",
		"codex resume 019e3627-68c2-72b2-a8c9-5fa2dbe03567":        "agent",
		"/home/alice/bin/codex-aarch64-a exec":                     "agent",
		"/Applications/Claude.app/Contents/MacOS/claude --verbose": "agent",
	}
	for command, want := range cases {
		if got := processCommandClass(command); got != want {
			t.Fatalf("processCommandClass(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestBuildProcessSnapshotMarksAgentRootsAndStatuses(t *testing.T) {
	processes := []processInfo{
		{pid: 10, ppid: 1, state: "S", command: "zsh"},
		{pid: 11, ppid: 10, state: "S", command: "codex exec"},
		{pid: 20, ppid: 1, state: "S", command: "zsh"},
		{pid: 21, ppid: 20, state: "R", command: "claude"},
	}
	snapshot := buildProcessSnapshot(processes)
	if !snapshot.roots[10] || !snapshot.roots[11] || !snapshot.roots[20] || !snapshot.roots[21] {
		t.Fatalf("agent roots not marked: %#v", snapshot.roots)
	}
	if snapshot.statuses[10] != agentStatusWaiting || snapshot.statuses[11] != agentStatusWaiting {
		t.Fatalf("sleeping codex tree should be waiting: %#v", snapshot.statuses)
	}
	if snapshot.statuses[20] != agentStatusWorking || snapshot.statuses[21] != agentStatusWorking {
		t.Fatalf("running claude tree should be working: %#v", snapshot.statuses)
	}
	if snapshot.names[10] != "codex" || snapshot.names[11] != "codex" {
		t.Fatalf("codex name should propagate to its pane root: %#v", snapshot.names)
	}
	if snapshot.names[20] != "claude" || snapshot.names[21] != "claude" {
		t.Fatalf("claude name should propagate to its pane root: %#v", snapshot.names)
	}
}

func stubDisplayTmux(t *testing.T, fn func(context.Context, string) (string, error)) func() {
	t.Helper()
	old := displayTmux
	displayTmux = fn
	return func() {
		displayTmux = old
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}
