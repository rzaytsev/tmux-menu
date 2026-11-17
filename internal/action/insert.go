package action

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"tmux-menu/internal/displaytext"
)

// insert delivers one literal, single-line paste to a frozen destination. It
// deliberately has no Enter/editing-key option and never falls back to a pane.
func insert(ctx context.Context, d Dispatch) error {
	if d.Cmd == "" || len(d.Cmd) > 64<<10 || !utf8.ValidString(d.Cmd) || strings.ContainsFunc(d.Cmd, displaytext.UnsafeControl) || d.Enter || d.ForegroundCommand == "" || d.WorkingDir == "" {
		return fmt.Errorf("invalid literal insertion payload or origin")
	}
	if err := revalidateInsert(ctx, d); err != nil {
		return err
	}
	buffer := newPasteBufferName()
	if err := tmuxExec(ctx, "set-buffer", "-b", buffer, "--", d.Cmd); err != nil {
		return err
	}
	// Always attempt cleanup, even after cancellation of the picker command.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tmuxExec(cleanup, "delete-buffer", "-b", buffer)
	}()
	if err := revalidateInsert(ctx, d); err != nil {
		return err
	}
	return tmuxExec(ctx, "paste-buffer", "-p", "-r", "-b", buffer, "-t", d.PaneID)
}

func revalidateInsert(ctx context.Context, d Dispatch) error {
	if err := revalidateSwitchPane(ctx, d); err != nil {
		return err
	}
	mode, err := tmuxRun(ctx, "display-message", "-p", "-t", d.PaneID, "#{bracket_paste_flag}:#{pane_in_mode}")
	if err != nil {
		return err
	}
	if mode != "1:0" {
		return fmt.Errorf("completion requires an input with bracketed paste enabled, outside copy mode; use Ctrl-Y to copy instead")
	}
	return nil
}
