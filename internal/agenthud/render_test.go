package agenthud

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"tmux-menu/internal/agentstatus"
)

func TestRenderEmptyStateAndEssentialControls(t *testing.T) {
	model := NewModel(ModelOptions{Width: 52, Height: 12})
	view := Render(model)
	for _, want := range []string{"Waiting for live agents", "/ picker", "agents --picker", "q quit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty view missing %q:\n%s", want, view)
		}
	}
	assertRenderBounds(t, view, 52, 12)
}

func TestRenderShowsNonColorStatusSummaryAndHonestTelemetry(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	model := NewModel(ModelOptions{Width: 120, Height: 30, Now: now})
	agent := testAgent(t, "%1", agentstatus.StateAttention)
	agent = NewAgent(AgentPresentation{
		Identity:       agent.Identity(),
		ProviderKind:   agentstatus.ProviderCodex,
		Provider:       SanitizeText("codex", 20),
		Status:         agentstatus.StateAttention,
		Session:        SanitizeText("work", 20),
		Thread:         SanitizeText("fix hud", 40),
		Workdir:        SanitizeText("tmux-menu", 40),
		Children:       2,
		TurnStartedAt:  now.Add(-2 * time.Minute),
		StateChangedAt: now.Add(-30 * time.Second),
		LastEventAt:    now.Add(-5 * time.Second),
	})
	applyAgents(t, &model, []Agent{agent, testAgent(t, "%2", agentstatus.StateWaiting)})
	view := Render(model)
	plainView := ansi.Strip(view)
	for _, want := range []string{"! attention", "○ waiting", "turn 2m", "state 30s", "event 5s", "children 2"} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	assertPassiveANSIOnly(t, view)
	assertRenderBounds(t, view, 120, 30)

	unknown := NewModel(ModelOptions{Width: 80, Height: 20, Now: now})
	applyAgents(t, &unknown, []Agent{testAgent(t, "%3", agentstatus.StateWorking)})
	if got := Render(unknown); strings.Contains(got, "turn ") || strings.Contains(got, "state ") || strings.Contains(got, "event ") {
		t.Fatalf("unsupported telemetry was invented:\n%s", got)
	}
}

func TestThemeSanitizesConfiguredMarkersAndBuildsOnlyAllowlistedStyles(t *testing.T) {
	theme, err := NewTheme(ThemeConfig{
		CodexIcon: "C\x1b]0;owned\a", ClaudeIcon: "L", CurrentIcon: "*", AttentionIcon: "A\x1b[2J",
		WorkingIcon: "W", CompletedIcon: "D", WaitingIcon: "I", UnknownIcon: "U",
		CodexColor: "blue", ClaudeColor: "orange", OtherColor: "magenta", ThreadColor: "bright_cyan", WorkdirColor: "dim",
		AttentionColor: "red", WorkingColor: "green", CompletedColor: "cyan", WaitingColor: "yellow", UnknownColor: "dim",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseColor("javascript"); err == nil {
		t.Fatal("invalid configured color was accepted")
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	model := NewModel(ModelOptions{Width: 100, Height: 20, Now: now, Theme: theme})
	agent := testAgent(t, "%1", agentstatus.StateAttention)
	agent = NewAgent(AgentPresentation{
		Identity: agent.Identity(), ProviderKind: agentstatus.ProviderCodex, Provider: SanitizeText("codex", 20),
		Status: agentstatus.StateAttention, Session: SanitizeText("work", 20), Thread: SanitizeText("fix hud", 30),
		Workdir: SanitizeText("tmux-menu", 30), CurrentThread: true, SessionColor: ColorOrange,
	})
	applyAgents(t, &model, []Agent{agent})
	view := Render(model)
	plainView := ansi.Strip(view)
	for _, want := range []string{"A attention", "C codex", "*fix hud"} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("themed view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "38;5;208m") {
		t.Fatalf("orange session style missing: %q", view)
	}
	assertPassiveANSIOnly(t, view)
}

func TestRenderNarrowAndHelpViewsRetainHierarchy(t *testing.T) {
	model := NewModel(ModelOptions{Width: 30, Height: 8})
	applyAgents(t, &model, []Agent{testAgent(t, "%1", agentstatus.StateWorking), testAgent(t, "%2", agentstatus.StateAttention)})
	model.HandleKey(KeyHelp)
	view := Render(model)
	plainView := ansi.Strip(view)
	for _, want := range []string{"Agents 2", "! 1", "/ picker", "enter switch", "q quit"} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("minimum view missing %q:\n%s", want, view)
		}
	}
	assertRenderBounds(t, view, 30, 8)
}

func TestRenderMarksPreservedTailStaleWithSanitizedFailure(t *testing.T) {
	model := NewModel(ModelOptions{Width: 80, Height: 20})
	first := model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{Generation: first.Generation, Agents: []Agent{testAgent(t, "%1", agentstatus.StateWorking)}, Captures: []TerminalUpdate{testTail(t, "%1", "safe tail")}})
	second := model.BeginRefresh()
	model.ApplyRefresh(RefreshResult{Generation: second.Generation, Failed: true, Failure: SanitizeText("timeout\x1b[2J", 40)})
	view := Render(model)
	if !strings.Contains(view, "stale") || !strings.Contains(view, "safe tail") || !strings.Contains(view, "timeout") {
		t.Fatalf("stale view missing evidence:\n%s", view)
	}
	assertPassiveANSIOnly(t, view)
}

func assertRenderBounds(t *testing.T, value string, width, height int) {
	t.Helper()
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		t.Fatalf("rendered %d lines, height %d", len(lines), height)
	}
	for index, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width %d > %d: %q", index, got, width, line)
		}
	}
}
