package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/programstatus"
)

type agentExitError struct{ code int }

func (e agentExitError) Error() string { return fmt.Sprintf("agent exited with status %d", e.code) }

// A single writer serializes keyboard input with locally generated probe replies.
type lockedPTY struct {
	sync.Mutex
	io.Writer
}

func (w *lockedPTY) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.Writer.Write(p)
}

func runAgentCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tmux-menu agent-run <codex|claude> [agent arguments...]")
	}
	provider, err := agentstatus.ParseProvider(args[0])
	if err != nil {
		return err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("agent-run requires an interactive terminal")
	}
	claim, owned, err := resolveAgentHookPane(ctx)
	if err != nil {
		return err
	}
	if !owned {
		return errors.New("agent-run must be started inside the target tmux pane")
	}
	// Only an ancestor may claim the pane; inherited TMUX_PANE alone is not proof.
	out, err := agentHookProcessOutput(ctx)
	if err != nil {
		return err
	}
	processes := parseProcessList(string(out))
	parents := map[int]int{}
	for _, p := range processes {
		parents[p.pid] = p.ppid
	}
	if !processDescendsFrom(parents, os.Getpid(), claim.PanePID) {
		return errors.New("agent-run does not own the target tmux pane")
	}
	store, err := openDefaultAgentStatusStore()
	if err != nil {
		return err
	}
	ws, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return err
	}
	var suspendMu sync.Mutex
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() { suspendMu.Lock(); defer suspendMu.Unlock(); _ = term.Restore(int(os.Stdin.Fd()), old) }()
	cmd := exec.Command(string(provider), args[1:]...)
	ptmx, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		return err
	}
	defer ptmx.Close()
	input := &lockedPTY{Writer: ptmx}
	value := agentstatus.OSCSnapshot{Version: 1, Pane: claim, Provider: provider, WrapperPID: os.Getpid(), ChildPID: cmd.Process.Pid, UpdatedAt: time.Now()}
	if err := store.WriteOSC(value); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("initialize OSC status: %w", err)
	}
	// Coalesce reports off the terminal path. Filesystem I/O never delays a
	// capability reply or stalls rendering; the latest snapshot always wins.
	updates := make(chan agentstatus.OSCSnapshot, 1)
	written := make(chan struct{})
	go func() {
		defer close(written)
		warned := false
		for update := range updates {
			if err := store.WriteOSC(update); err != nil && !warned {
				fmt.Fprint(os.Stderr, "\r\ntmux-menu: OSC status storage unavailable; terminal remains usable\r\n")
				warned = true
			}
		}
	}()
	stream := &programstatus.Stream{Output: os.Stdout, Reply: input}
	stream.Changed = func() {
		value.Records = stream.Machine.Snapshot()
		value.LastWorkingAt = stream.Machine.LastWorkingAt
		value.UpdatedAt = time.Now()
		select {
		case <-updates:
		default:
		}
		updates <- value
	}
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGWINCH, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT, syscall.SIGTSTP, syscall.SIGCONT)
	defer signal.Stop(signals)
	suspend := func() {
		suspendMu.Lock()
		defer suspendMu.Unlock()
		_ = term.Restore(int(os.Stdin.Fd()), old)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGSTOP)
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
		// Execution resumes here when the shell brings the wrapper back with fg.
		_, _ = term.MakeRaw(int(os.Stdin.Fd()))
		_ = pty.InheritSize(os.Stdin, ptmx)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT)
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			start := 0
			for i, c := range buf[:n] {
				if c == 26 { // Ctrl-Z suspends both PTYs, rather than stranding the child.
					if _, writeErr := input.Write(buf[start:i]); writeErr != nil {
						return
					}
					suspend()
					start = i + 1
				}
			}
			if _, writeErr := input.Write(buf[start:n]); writeErr != nil {
				return
			}
			if err != nil {
				return
			}
		}
	}()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
				return
			case sig := <-signals:
				if sig == syscall.SIGTSTP {
					suspend()
				} else if sig == syscall.SIGCONT {
					_ = pty.InheritSize(os.Stdin, ptmx)
				} else if sig == syscall.SIGWINCH {
					_ = pty.InheritSize(os.Stdin, ptmx)
				} else {
					_ = syscall.Kill(-cmd.Process.Pid, sig.(syscall.Signal))
				}
			}
		}
	}()
	// Wait independently of the PTY reader: a grandchild holding the slave open
	// must not prevent exit and terminal-mode restoration after the agent exits.
	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(stream, ptmx); _ = stream.Flush(); close(readDone) }()
	err = cmd.Wait()
	select {
	case <-readDone:
	case <-time.After(250 * time.Millisecond):
		_ = ptmx.Close()
		<-readDone
	}
	stream.Machine.Prompt() // process exit drops working/blocked; retain done/error.
	stream.Changed()
	close(updates)
	<-written
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code := exit.ExitCode()
			if code < 0 {
				if status, ok := exit.Sys().(syscall.WaitStatus); ok {
					code = 128 + int(status.Signal())
				}
			}
			return agentExitError{code: code}
		}
	}
	return err
}

func processDescendsFrom(parents map[int]int, pid, ancestor int) bool {
	for steps := 0; pid > 0 && steps < 256; steps++ {
		if pid == ancestor {
			return true
		}
		next := parents[pid]
		if next == pid {
			return false
		}
		pid = next
	}
	return false
}
