package agenthud

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tmux-menu/internal/agentstatus"
)

func TestRuntimeRefreshesSeriallyAndSchedulesOnlyAfterCompletion(t *testing.T) {
	var mu sync.Mutex
	active := 0
	maxActive := 0
	calls := 0
	refresh := func(_ context.Context, request RefreshRequest) (RefreshResult, error) {
		mu.Lock()
		active++
		calls++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		result := RefreshResult{Generation: request.Generation, Agents: []Agent{testAgent(t, "%1", agentstatus.StateWorking)}}
		for _, target := range request.Targets {
			result.Captures = append(result.Captures, TerminalUpdate{
				Identity: target.Identity,
				Terminal: SanitizeTerminal([]byte("live"), TerminalLimits{Width: target.Width, Height: target.Height, MaxInputBytes: 64, MaxRetainedBytes: 64}),
			})
		}
		mu.Lock()
		active--
		mu.Unlock()
		return result, nil
	}
	delays := make(chan time.Duration, 2)
	runtime := NewRuntime(t.Context(), NewModel(ModelOptions{Width: 80, Height: 20}), refresh, RuntimeOptions{
		RefreshInterval: time.Second,
		Delay: func(_ context.Context, delay time.Duration) tea.Cmd {
			delays <- delay
			return func() tea.Msg { return refreshTickMsg{} }
		},
	})

	first := runtime.Init()
	if first == nil {
		t.Fatal("runtime did not start initial refresh")
	}
	message := first()
	_, afterFirst := runtime.Update(message)
	if afterFirst == nil || <-delays != 0 {
		t.Fatal("newly discovered visible pane did not request immediate capture generation")
	}
	_, second := runtime.Update(afterFirst())
	if second == nil {
		t.Fatal("tick did not start next refresh")
	}
	_, afterSecond := runtime.Update(second())
	if afterSecond == nil || <-delays != time.Second {
		t.Fatal("completed capture did not schedule normal refresh interval")
	}
	if calls != 2 || maxActive != 1 {
		t.Fatalf("refresh calls=%d max active=%d", calls, maxActive)
	}
}

func TestRuntimeMapsTeaKeysToExplicitActionAndCancelsRoot(t *testing.T) {
	model := NewModel(ModelOptions{Width: 80, Height: 20})
	applyAgents(t, &model, []Agent{testAgent(t, "%1", agentstatus.StateWorking)})
	runtime := NewRuntime(t.Context(), model, nil, RuntimeOptions{})
	returned, command := runtime.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := returned.(*Runtime)
	if got.Action().Kind() != ActionSwitch || got.Action().Identity().PaneID() != "%1" || command == nil {
		t.Fatalf("enter result action=%#v cmd=%v", got.Action(), command)
	}
	command()
	select {
	case <-got.Context().Done():
	default:
		t.Fatal("terminal action did not cancel root context")
	}
}

func TestRuntimeTerminalActionCancelsBlockedRefresh(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan error, 1)
	runtime := NewRuntime(t.Context(), NewModel(ModelOptions{Width: 80, Height: 20}), func(ctx context.Context, request RefreshRequest) (RefreshResult, error) {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return RefreshResult{Generation: request.Generation}, ctx.Err()
	}, RuntimeOptions{})
	command := runtime.Init()
	finished := make(chan tea.Msg, 1)
	go func() { finished <- command() }()
	<-started
	runtime.Update(tea.KeyPressMsg(tea.Key{Code: 'q'}))
	select {
	case err := <-stopped:
		if err != context.Canceled {
			t.Fatalf("blocked refresh stopped with %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("terminal action did not cancel blocked refresh")
	}
	select {
	case <-finished:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("cancelled refresh command did not return")
	}
}

func TestRuntimeViewUsesAlternateScreenAndResizeIsPure(t *testing.T) {
	runtime := NewRuntime(t.Context(), NewModel(ModelOptions{Width: 30, Height: 8}), nil, RuntimeOptions{})
	updated, cmd := runtime.Update(tea.WindowSizeMsg{Width: 52, Height: 22})
	if cmd != nil {
		t.Fatalf("resize emitted command %v", cmd)
	}
	got := updated.(*Runtime)
	view := got.View()
	if !view.AltScreen || got.Core().Size() != (Size{Width: 52, Height: 22}) {
		t.Fatalf("view alt=%v size=%#v", view.AltScreen, got.Core().Size())
	}
}

func TestRuntimeSanitizesRefreshErrorsBeforeModelState(t *testing.T) {
	runtime := NewRuntime(t.Context(), NewModel(ModelOptions{Width: 80, Height: 20}), func(_ context.Context, request RefreshRequest) (RefreshResult, error) {
		return RefreshResult{Generation: request.Generation}, testError("bad\x1b]52;c;owned\a")
	}, RuntimeOptions{Delay: func(_ context.Context, _ time.Duration) tea.Cmd { return nil }})
	message := runtime.Init()()
	runtime.Update(message)
	view := runtime.View().Content
	if !stringsContains(view, "bad") {
		t.Fatalf("sanitized error not shown: %q", view)
	}
	assertPassiveANSIOnly(t, view)
}

type testError string

func (e testError) Error() string { return string(e) }

func stringsContains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
