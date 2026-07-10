package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tmux-menu/internal/config"
	"tmux-menu/internal/shellquote"
	"tmux-menu/internal/tmux"
)

var (
	tmuxRun                 = tmux.Run
	tmuxExec                = tmux.Exec
	shellRunner             = runShell
	attachTmuxSessionRunner = attachTmuxSession
	projectBootstrapTimeout = 30 * time.Second
	newPasteBufferName      = func() string {
		return fmt.Sprintf("tmux-menu-paste-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
)

type Dispatch struct {
	Mode               string     `json:"mode"`
	Cmd                string     `json:"cmd,omitempty"`
	PaneID             string     `json:"pane_id,omitempty"`
	WindowID           string     `json:"window_id,omitempty"`
	SessionID          string     `json:"session_id,omitempty"`
	WindowName         string     `json:"window_name,omitempty"`
	WorkingDir         string     `json:"working_dir,omitempty"`
	ProjectPath        string     `json:"project_path,omitempty"`
	ProjectSessionName string     `json:"project_session_name,omitempty"`
	BootstrapFile      string     `json:"bootstrap_file,omitempty"`
	Enter              bool       `json:"enter,omitempty"`
	SplitHorizontal    bool       `json:"split_horizontal,omitempty"`
	PaneSide           string     `json:"pane_side,omitempty"`
	PopupWidth         string     `json:"popup_width,omitempty"`
	PopupHeight        string     `json:"popup_height,omitempty"`
	PopupBorder        string     `json:"popup_border,omitempty"`
	Steps              []Dispatch `json:"steps,omitempty"`
}

func FromCommand(c config.Command, popup config.PopupConfig, originPath string) Dispatch {
	wd := c.WorkingDir
	if wd == "" {
		wd = originPath
	}
	return Dispatch{
		Mode:        c.Mode,
		Cmd:         c.Cmd,
		WindowName:  c.WindowName,
		WorkingDir:  wd,
		Enter:       c.Enter,
		PopupWidth:  popup.Width,
		PopupHeight: popup.Height,
		PopupBorder: popup.Border,
	}
}

func SwitchPane(p tmux.Pane) Dispatch {
	return Dispatch{
		Mode:      "switch-pane",
		PaneID:    p.PaneID,
		WindowID:  p.WindowID,
		SessionID: p.SessionID,
	}
}

func SwitchSession(p tmux.Pane) Dispatch {
	return Dispatch{
		Mode:      "switch-session",
		SessionID: p.SessionID,
	}
}

func Project(path string, bootstrapFile string) Dispatch {
	return ProjectWithSessionName(path, bootstrapFile, projectSessionName(path))
}

func ProjectWithSessionName(path string, bootstrapFile string, sessionName string) Dispatch {
	return Dispatch{
		Mode:               "project",
		ProjectPath:        path,
		ProjectSessionName: sessionName,
		BootstrapFile:      bootstrapFile,
	}
}

func Write(path string, d Dispatch) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func Read(path string) (Dispatch, error) {
	var d Dispatch
	b, err := os.ReadFile(path)
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	return d, nil
}

func Execute(ctx context.Context, d Dispatch, originPane string) error {
	switch d.Mode {
	case "":
		return nil
	case "sequence":
		for _, step := range d.Steps {
			if err := Execute(ctx, step, originPane); err != nil {
				return err
			}
		}
		return nil
	case "switch-pane":
		return tmuxExec(ctx,
			"switch-client", "-t", d.SessionID,
			";", "select-window", "-t", d.WindowID,
			";", "select-pane", "-t", d.PaneID,
		)
	case "switch-session":
		return tmuxExec(ctx, "switch-client", "-t", d.SessionID)
	case "project":
		return openProject(ctx, d)
	case "popup":
		return tmuxExec(ctx, popupArgs(d)...)
	case "pane":
		return tmuxExec(ctx, paneArgs(d, originPane)...)
	case "paste":
		if err := paste(ctx, originPane, d.Cmd, d.Enter); err != nil {
			return err
		}
		return nil
	case "window":
		args := []string{"new-window"}
		if d.WorkingDir != "" {
			args = append(args, "-c", d.WorkingDir)
		}
		if d.WindowName != "" {
			args = append(args, "-n", d.WindowName)
		}
		args = append(args, d.Cmd)
		return tmuxExec(ctx, args...)
	case "tmux":
		return shellRunner(ctx, "tmux "+d.Cmd)
	case "shell":
		return shellRunner(ctx, d.Cmd)
	default:
		return fmt.Errorf("unknown dispatch mode %q", d.Mode)
	}
}

func popupArgs(d Dispatch) []string {
	args := []string{"display-popup", "-E"}
	if d.PopupBorder == "" || d.PopupBorder == "none" {
		args = append(args, "-B")
	} else {
		args = append(args, "-b", d.PopupBorder)
	}
	if d.PopupWidth != "" {
		args = append(args, "-w", d.PopupWidth)
	}
	if d.PopupHeight != "" {
		args = append(args, "-h", d.PopupHeight)
	}
	if d.WorkingDir != "" {
		args = append(args, "-d", d.WorkingDir)
	}
	args = append(args, d.Cmd)
	return args
}

func paneArgs(d Dispatch, originPane string) []string {
	args := []string{"split-window"}
	switch strings.TrimSpace(d.PaneSide) {
	case "left":
		args = append(args, "-h", "-b")
	case "right":
		args = append(args, "-h")
	case "above":
		args = append(args, "-b")
	case "below":
	default:
		if d.SplitHorizontal {
			args = append(args, "-h")
		}
	}
	if originPane != "" {
		args = append(args, "-t", originPane)
	}
	if d.WorkingDir != "" {
		args = append(args, "-c", d.WorkingDir)
	}
	args = append(args, d.Cmd)
	return args
}

func paste(ctx context.Context, paneID string, text string, enter bool) error {
	bufferName := newPasteBufferName()
	args := []string{"set-buffer", "-b", bufferName, "--", text}
	if err := tmuxExec(ctx, args...); err != nil {
		return err
	}
	var opErr error
	pasteArgs := []string{"paste-buffer", "-b", bufferName}
	if paneID != "" {
		pasteArgs = append(pasteArgs, "-t", paneID)
	}
	if err := tmuxExec(ctx, pasteArgs...); err != nil {
		opErr = err
	}
	if opErr == nil && enter {
		sendArgs := []string{"send-keys"}
		if paneID != "" {
			sendArgs = append(sendArgs, "-t", paneID)
		}
		sendArgs = append(sendArgs, "Enter")
		opErr = tmuxExec(ctx, sendArgs...)
	}
	cleanupErr := tmuxExec(ctx, "delete-buffer", "-b", bufferName)
	if opErr != nil {
		return opErr
	}
	return cleanupErr
}

func runShell(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, "sh", "-lc", command)
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func openProject(ctx context.Context, d Dispatch) error {
	root := strings.TrimSpace(d.ProjectPath)
	if root == "" {
		return fmt.Errorf("project_path is required")
	}
	sessionName := projectSessionName(root)
	if d.ProjectSessionName != "" {
		sessionName = d.ProjectSessionName
	}
	created := false
	if err := tmuxExec(ctx, "has-session", "-t="+sessionName); err != nil {
		if err := tmuxExec(ctx, "new-session", "-ds", sessionName, "-c", root); err != nil {
			return err
		}
		created = true
	}
	if created {
		if err := runProjectBootstrap(ctx, sessionName, root, d.BootstrapFile); err != nil {
			return err
		}
	}
	if os.Getenv("TMUX") != "" {
		return tmuxExec(ctx, "switch-client", "-t", sessionName)
	}
	return attachTmuxSessionRunner(ctx, sessionName)
}

func runProjectBootstrap(ctx context.Context, sessionName string, root string, bootstrapFile string) error {
	bootstrapPath := projectBootstrapPath(root, bootstrapFile)
	if _, err := os.Stat(bootstrapPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	out, err := tmuxRun(ctx, "list-panes", "-t", sessionName, "-F", "#{pane_id}")
	if err != nil {
		return err
	}
	targetPane := firstNonEmptyLine(out)
	if targetPane == "" {
		return fmt.Errorf("failed to find bootstrap pane for session: %s", sessionName)
	}
	waitToken := fmt.Sprintf("tmux-menu-bootstrap-%s-%d", sessionName, time.Now().UnixNano())
	command := projectBootstrapCommand(root, bootstrapPath, waitToken)
	if err := tmuxExec(ctx, "send-keys", "-t", targetPane, command, "Enter"); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, projectBootstrapTimeout)
	defer cancel()
	if err := tmuxExec(waitCtx, "wait-for", waitToken); err != nil {
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("project bootstrap timed out after %s: %w", projectBootstrapTimeout, err)
		}
		return err
	}
	return nil
}

func projectBootstrapPath(root string, bootstrapFile string) string {
	bootstrapFile = strings.TrimSpace(bootstrapFile)
	if bootstrapFile == "" {
		bootstrapFile = ".tmux-sessionizer"
	}
	if filepath.IsAbs(bootstrapFile) {
		return bootstrapFile
	}
	return filepath.Join(root, bootstrapFile)
}

func projectSessionName(root string) string {
	return strings.ReplaceAll(filepath.Base(root), ".", "_")
}

func ProjectSessionName(root string) string {
	return projectSessionName(root)
}

func UniqueProjectSessionName(root string) string {
	base := projectSessionName(root)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "project"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(filepath.Clean(root)))
	return fmt.Sprintf("%s_%08x", base, h.Sum32())
}

func projectBootstrapCommand(root string, bootstrapPath string, waitToken string) string {
	return "cd " + shellquote.Quote(root) + " && bash " + shellquote.Quote(bootstrapPath) + "; tmux wait-for -S " + shellquote.Quote(waitToken)
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func attachTmuxSession(ctx context.Context, sessionName string) error {
	cmd := exec.CommandContext(ctx, "tmux", "attach-session", "-t", sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
