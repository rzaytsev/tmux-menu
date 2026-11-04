package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"tmux-menu/internal/displaytext"
	"tmux-menu/internal/tmux"
)

func printAgentSelectionDetails(ctx context.Context, out io.Writer, pane tmux.Pane, row agentPreviewData) {
	fmt.Fprintln(out, displaytext.Line("Thread: "+agentListPaneTitle(pane, row.Provider)))
	fmt.Fprintln(out, displaytext.Line("Workdir: "+pane.CurrentPath))
	fmt.Fprintln(out, displaytext.Line("Git: "+agentGitBranch(ctx, pane.CurrentPath)))
	fmt.Fprintln(out, displaytext.Line("Session: "+pane.SessionName+" · state age: "+nonEmpty(row.StateAge, "—")))
}

func agentGitBranch(ctx context.Context, dir string) string {
	if dir == "" {
		return "—"
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	budget := tmux.NewOutputBudget(4096)
	if branch, err := tmux.RunCommandBounded(ctx, budget, 2048, "git", "--no-optional-locks", "-C", dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(branch)
	}
	if head, err := tmux.RunCommandBounded(ctx, budget, 2048, "git", "--no-optional-locks", "-C", dir, "rev-parse", "--short", "HEAD"); err == nil {
		return "detached @ " + strings.TrimSpace(head)
	}
	return "—"
}
