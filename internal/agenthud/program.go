package agenthud

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	defaultRefreshInterval = time.Second
	refreshErrorWidth      = 160
)

type RefreshFunc func(context.Context, RefreshRequest) (RefreshResult, error)
type DelayFunc func(context.Context, time.Duration) tea.Cmd

type RuntimeOptions struct {
	RefreshInterval time.Duration
	Delay           DelayFunc
	Clock           func() time.Time
}

type refreshDoneMsg struct {
	result RefreshResult
}

type refreshTickMsg struct{}

type Runtime struct {
	rootCtx         context.Context
	cancel          context.CancelFunc
	core            Model
	refresh         RefreshFunc
	delay           DelayFunc
	clock           func() time.Time
	refreshInterval time.Duration
	active          bool
	action          Action
}

func NewRuntime(parent context.Context, model Model, refresh RefreshFunc, options RuntimeOptions) *Runtime {
	rootCtx, cancel := context.WithCancel(parent)
	interval := options.RefreshInterval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	delay := options.Delay
	if delay == nil {
		delay = delayWithContext
	}
	if model.now.IsZero() {
		model.SetNow(clock())
	}
	return &Runtime{
		rootCtx: rootCtx, cancel: cancel, core: model, refresh: refresh,
		delay: delay, clock: clock, refreshInterval: interval,
	}
}

func (r *Runtime) Init() tea.Cmd {
	return r.startRefresh()
}

func (r *Runtime) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case tea.WindowSizeMsg:
		r.core.SetSize(value.Width, value.Height)
		return r, nil
	case tea.KeyPressMsg:
		action := r.core.HandleKey(keyFromTea(value.String()))
		if action.Kind() != ActionNone {
			r.action = action
			r.cancel()
			return r, tea.Quit
		}
		return r, nil
	case refreshDoneMsg:
		r.active = false
		r.core.ApplyRefresh(value.result)
		delay := r.refreshInterval
		if r.core.NeedsVisibleCapture() {
			delay = 0
		}
		return r, r.delay(r.rootCtx, delay)
	case refreshTickMsg:
		return r, r.startRefresh()
	default:
		return r, nil
	}
}

func (r *Runtime) View() tea.View {
	view := tea.NewView(Render(r.core))
	view.AltScreen = true
	return view
}

func (r *Runtime) startRefresh() tea.Cmd {
	if r.active || r.refresh == nil || r.rootCtx.Err() != nil {
		return nil
	}
	r.active = true
	request := r.core.BeginRefresh()
	return func() tea.Msg {
		result, err := r.refresh(r.rootCtx, request)
		result.Generation = request.Generation
		if result.Now.IsZero() {
			result.Now = r.clock()
		}
		if err != nil {
			result.Failed = true
			result.Failure = SanitizeText(err.Error(), refreshErrorWidth)
		}
		return refreshDoneMsg{result: result}
	}
}

func delayWithContext(ctx context.Context, delay time.Duration) tea.Cmd {
	return func() tea.Msg {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return refreshTickMsg{}
		}
	}
}

func keyFromTea(value string) Key {
	switch value {
	case "left", "h":
		return Key(value)
	case "right", "l":
		return Key(value)
	case "up", "k":
		return Key(value)
	case "down", "j":
		return Key(value)
	case "[", "]", "n", "z", "?", "q", "enter", "/":
		return Key(value)
	default:
		return ""
	}
}

func (r *Runtime) Action() Action           { return r.action }
func (r *Runtime) Core() Model              { return r.core }
func (r *Runtime) Context() context.Context { return r.rootCtx }

type RunResult struct {
	Model  Model
	Action Action
}

func Run(ctx context.Context, model Model, refresh RefreshFunc, options RuntimeOptions) (RunResult, error) {
	runtime := NewRuntime(ctx, model, refresh, options)
	defer runtime.cancel()
	final, err := tea.NewProgram(runtime, tea.WithContext(ctx)).Run()
	if err != nil && !errors.Is(err, context.Canceled) {
		return RunResult{}, err
	}
	finished, ok := final.(*Runtime)
	if !ok {
		return RunResult{}, errors.New("agent HUD returned an unexpected model")
	}
	return RunResult{Model: finished.core, Action: finished.action}, nil
}
