package agenthud

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"tmux-menu/internal/tmux"
)

var (
	ErrTooManyVisiblePanes = errors.New("too many visible panes")
	ErrTerminalBudget      = errors.New("terminal content budget exceeded")
)

type GenerationLimits struct {
	Timeout             time.Duration
	MaxVisiblePanes     int
	CaptureLines        int
	CaptureBytesPerPane int64
	TotalOutputBytes    int64
	MaxTerminalBytes    int
	Terminal            TerminalLimits
}

type CaptureFunc func(context.Context, *tmux.OutputBudget, string, int, int64) (string, error)

type CaptureResult struct {
	PaneID   string
	Terminal Terminal
	Err      error
}

type Coordinator struct {
	mu     sync.Mutex
	active bool
	limits GenerationLimits
}

type Generation struct {
	ctx         context.Context
	cancel      context.CancelFunc
	budget      *tmux.OutputBudget
	coordinator *Coordinator
	limits      GenerationLimits
	done        sync.Once
}

func NewCoordinator(limits GenerationLimits) *Coordinator {
	return &Coordinator{limits: limits}
}

func (c *Coordinator) Begin(parent context.Context) (*Generation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active {
		return nil, false
	}
	c.active = true
	ctx, cancel := context.WithTimeout(parent, c.limits.Timeout)
	return &Generation{
		ctx:         ctx,
		cancel:      cancel,
		budget:      tmux.NewOutputBudget(c.limits.TotalOutputBytes),
		coordinator: c,
		limits:      c.limits,
	}, true
}

func (g *Generation) Context() context.Context {
	return g.ctx
}

func (g *Generation) OutputBudget() *tmux.OutputBudget {
	return g.budget
}

func (g *Generation) Done() {
	g.done.Do(func() {
		g.cancel()
		g.coordinator.mu.Lock()
		g.coordinator.active = false
		g.coordinator.mu.Unlock()
	})
}

func (g *Generation) CaptureVisible(paneIDs []string, capture CaptureFunc) ([]CaptureResult, error) {
	if len(paneIDs) > g.limits.MaxVisiblePanes {
		return nil, fmt.Errorf("%w: got %d, max %d", ErrTooManyVisiblePanes, len(paneIDs), g.limits.MaxVisiblePanes)
	}
	if capture == nil && len(paneIDs) != 0 {
		return nil, errors.New("capture function is required")
	}

	type rawResult struct {
		value string
		err   error
	}
	raw := make([]rawResult, len(paneIDs))
	var wait sync.WaitGroup
	for index, paneID := range paneIDs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			raw[index].value, raw[index].err = capture(
				g.ctx,
				g.budget,
				paneID,
				g.limits.CaptureLines,
				g.limits.CaptureBytesPerPane,
			)
		}()
	}
	wait.Wait()

	results := make([]CaptureResult, len(paneIDs))
	retained := 0
	for index, paneID := range paneIDs {
		results[index].PaneID = paneID
		if raw[index].err != nil {
			results[index].Err = raw[index].err
			continue
		}
		terminal := SanitizeTerminal([]byte(raw[index].value), g.limits.Terminal)
		if retained+terminal.RetainedBytes() > g.limits.MaxTerminalBytes {
			results[index].Err = ErrTerminalBudget
			continue
		}
		results[index].Terminal = terminal
		retained += terminal.RetainedBytes()
	}
	return results, nil
}
