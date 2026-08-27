package agenthud

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"tmux-menu/internal/tmux"
)

func TestCoordinatorRejectsOverlappingGenerationsAndCancelsOnRelease(t *testing.T) {
	coordinator := NewCoordinator(GenerationLimits{Timeout: time.Second, TotalOutputBytes: 1024})
	first, ok := coordinator.Begin(t.Context())
	if !ok {
		t.Fatal("first generation was rejected")
	}
	if _, ok := coordinator.Begin(t.Context()); ok {
		t.Fatal("overlapping generation was admitted")
	}
	first.Done()
	select {
	case <-first.Context().Done():
	default:
		t.Fatal("released generation context is still live")
	}
	second, ok := coordinator.Begin(t.Context())
	if !ok {
		t.Fatal("next serial generation was rejected")
	}
	second.Done()
}

func TestGenerationDeadlineIsSharedAcrossOperations(t *testing.T) {
	coordinator := NewCoordinator(GenerationLimits{Timeout: 30 * time.Millisecond, TotalOutputBytes: 1024})
	generation, ok := coordinator.Begin(t.Context())
	if !ok {
		t.Fatal("generation rejected")
	}
	defer generation.Done()

	<-generation.Context().Done()
	started := time.Now()
	_, err := tmux.RunCommandBounded(generation.Context(), generation.OutputBudget(), 128, "sh", "-c", "printf never")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("expired shared deadline returned err=%v after %s", err, time.Since(started))
	}
}

func TestGenerationDoneCancelsInFlightVisibleCapture(t *testing.T) {
	coordinator := NewCoordinator(GenerationLimits{
		Timeout:             time.Second,
		MaxVisiblePanes:     1,
		CaptureLines:        20,
		CaptureBytesPerPane: 256,
		TotalOutputBytes:    1024,
		MaxTerminalBytes:    256,
		Terminal:            TerminalLimits{Width: 20, Height: 4, MaxInputBytes: 256, MaxRetainedBytes: 128},
	})
	generation, _ := coordinator.Begin(t.Context())
	started := make(chan struct{})
	finished := make(chan []CaptureResult, 1)
	go func() {
		results, _ := generation.CaptureVisible([]string{"%2"}, func(ctx context.Context, _ *tmux.OutputBudget, _ string, _ int, _ int64) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		})
		finished <- results
	}()
	<-started
	generation.Done()
	select {
	case results := <-finished:
		if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
			t.Fatalf("cancelled capture results = %#v", results)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("in-flight capture did not stop after generation cancellation")
	}
}

func TestCaptureVisibleOnlyTargetsInputsAndKeepsPerPaneErrors(t *testing.T) {
	coordinator := NewCoordinator(GenerationLimits{
		Timeout:             time.Second,
		MaxVisiblePanes:     4,
		CaptureLines:        20,
		CaptureBytesPerPane: 256,
		TotalOutputBytes:    1024,
		MaxTerminalBytes:    512,
		Terminal:            TerminalLimits{Width: 20, Height: 4, MaxInputBytes: 256, MaxRetainedBytes: 128},
	})
	generation, ok := coordinator.Begin(t.Context())
	if !ok {
		t.Fatal("generation rejected")
	}
	defer generation.Done()

	var mu sync.Mutex
	var captured []string
	results, err := generation.CaptureVisible([]string{"%2", "%4"}, func(ctx context.Context, budget *tmux.OutputBudget, paneID string, lines int, maxBytes int64) (string, error) {
		mu.Lock()
		captured = append(captured, paneID)
		mu.Unlock()
		if paneID == "%4" {
			return "", errors.New("pane vanished")
		}
		return "live\noutput", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 2 || !containsString(captured, "%2") || !containsString(captured, "%4") || containsString(captured, "%1") {
		t.Fatalf("captured panes = %#v", captured)
	}
	if len(results) != 2 || results[0].PaneID != "%2" || results[0].Terminal.Plain() != "live\noutput" || results[0].Err != nil {
		t.Fatalf("first capture = %#v", results[0])
	}
	if results[1].PaneID != "%4" || results[1].Err == nil || results[1].Terminal.RetainedBytes() != 0 {
		t.Fatalf("vanished capture = %#v", results[1])
	}
}

func TestCaptureVisibleRejectsTooManyPanesAndAggregateOverflow(t *testing.T) {
	limits := GenerationLimits{
		Timeout:             time.Second,
		MaxVisiblePanes:     2,
		CaptureLines:        20,
		CaptureBytesPerPane: 64,
		TotalOutputBytes:    128,
		MaxTerminalBytes:    5,
		Terminal:            TerminalLimits{Width: 20, Height: 4, MaxInputBytes: 64, MaxRetainedBytes: 64},
	}
	coordinator := NewCoordinator(limits)
	generation, _ := coordinator.Begin(t.Context())
	defer generation.Done()

	if _, err := generation.CaptureVisible([]string{"%1", "%2", "%3"}, nil); !errors.Is(err, ErrTooManyVisiblePanes) {
		t.Fatalf("too many panes error = %v", err)
	}
	results, err := generation.CaptureVisible([]string{"%1", "%2"}, func(context.Context, *tmux.OutputBudget, string, int, int64) (string, error) {
		return "four", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Err != nil || !errors.Is(results[1].Err, ErrTerminalBudget) || results[1].Terminal.RetainedBytes() != 0 {
		t.Fatalf("aggregate capture results = %#v", results)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
