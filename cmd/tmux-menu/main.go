package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
	"tmux-menu/internal/tmux"
)

type menuItem struct {
	dispatch          action.Dispatch
	alternateKey      string
	alternateDispatch action.Dispatch
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tmux-menu:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "popup":
		return runPopup(ctx, args[1:])
	case "palette":
		return runPickerLoop(ctx, "palette")
	case "agents":
		return runPickerLoop(ctx, "agents")
	case "tools":
		return runPickerLoop(ctx, "tools")
	case "projects":
		return runPickerLoop(ctx, "projects")
	case "links":
		return runPickerLoop(ctx, "links")
	case "bookmarks":
		return runPickerLoop(ctx, "bookmarks")
	case "status":
		return runPickerLoop(ctx, "status")
	case "validate-config":
		paths := args[1:]
		if len(paths) == 0 {
			paths = []string{config.DefaultPath()}
		}
		if _, err := config.LoadStrict(paths...); err != nil {
			return err
		}
		fmt.Printf("config valid: %s\n", strings.Join(paths, ", "))
		return nil
	case "sample-config":
		fmt.Print(config.Sample())
		return nil
	case "help", "-h", "--help":
		return usage()
	default:
		return usage()
	}
}

func usage() error {
	fmt.Fprint(os.Stderr, `tmux-menu

Usage:
  tmux-menu popup palette
  tmux-menu popup agents
  tmux-menu popup tools
  tmux-menu popup projects
  tmux-menu popup links
  tmux-menu popup bookmarks
  tmux-menu popup status
  tmux-menu palette
  tmux-menu agents
  tmux-menu tools
  tmux-menu projects
  tmux-menu links
  tmux-menu bookmarks
  tmux-menu status
  tmux-menu validate-config [config ...]
  tmux-menu sample-config
`)
	return nil
}

func runPopup(ctx context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{"palette"}
	}
	mode := args[0]
	if !validViewMode(mode) {
		return fmt.Errorf("popup mode must be palette, agents, tools, projects, links, bookmarks, or status")
	}
	rt, err := loadRuntimeContext(ctx)
	if err != nil {
		return err
	}
	cfg, err := config.LoadForContext(rt.OriginPath, rt.SessionPath)
	if err != nil {
		return err
	}
	dispatchFile, err := os.CreateTemp("", "tmux-menu-dispatch-*.json")
	if err != nil {
		return err
	}
	dispatchPath := dispatchFile.Name()
	_ = dispatchFile.Close()
	defer os.Remove(dispatchPath)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)

	childArgs := append([]string{mode}, args[1:]...)
	cmd := buildPopupCommand(exe, dispatchPath, rt, childArgs)
	tmuxArgs := []string{"display-popup", "-E"}
	if cfg.Popup.Border == "" || cfg.Popup.Border == "none" {
		tmuxArgs = append(tmuxArgs, "-B")
	} else {
		tmuxArgs = append(tmuxArgs, "-b", cfg.Popup.Border)
	}
	if cfg.Popup.Width != "" {
		tmuxArgs = append(tmuxArgs, "-w", cfg.Popup.Width)
	}
	if cfg.Popup.Height != "" {
		tmuxArgs = append(tmuxArgs, "-h", cfg.Popup.Height)
	}
	tmuxArgs = append(tmuxArgs, cmd)
	if err := tmux.Exec(ctx, tmuxArgs...); err != nil {
		return err
	}
	if info, err := os.Stat(dispatchPath); err == nil && info.Size() > 0 {
		d, err := action.Read(dispatchPath)
		if err != nil {
			return err
		}
		return action.Execute(ctx, d, rt.OriginPane)
	}
	return nil
}

func buildPopupCommand(exe, dispatchPath string, rt runtimeContext, args []string) string {
	parts := []string{
		"TMUX_MENU_DISPATCH_FILE=" + shellquote.Quote(dispatchPath),
		"TMUX_MENU_ORIGIN_PANE=" + shellquote.Quote(rt.OriginPane),
		"TMUX_MENU_ORIGIN_PATH=" + shellquote.Quote(rt.OriginPath),
		"TMUX_MENU_SESSION_ID=" + shellquote.Quote(rt.SessionID),
		"TMUX_MENU_SESSION_NAME=" + shellquote.Quote(rt.SessionName),
		"TMUX_MENU_SESSION_PATH=" + shellquote.Quote(rt.SessionPath),
		"exec",
		shellquote.Quote(exe),
	}
	for _, arg := range args {
		parts = append(parts, shellquote.Quote(arg))
	}
	return strings.Join(parts, " ")
}

var viewSwitchKeys = []string{"tab", "btab", "alt-1", "alt-2", "alt-3", "alt-4", "alt-5", "alt-6"}

const viewSwitchHelp = "Tab:Next  Shift-Tab:Previous | Alt+ 1:Main  2:Agents  3:Tools  4:Projects  5:Status  6:Bookmarks"

func viewSwitchFooter() string {
	return viewSwitchHelp
}

func pickerPreviewWindow(width string, options ...string) string {
	parts := []string{"right", width}
	parts = append(parts, options...)
	return strings.Join(parts, ":")
}

func viewModeForKey(key string) string {
	switch key {
	case "alt-1":
		return "palette"
	case "alt-2":
		return "agents"
	case "alt-3":
		return "tools"
	case "alt-4":
		return "projects"
	case "alt-5":
		return "status"
	case "alt-6":
		return "bookmarks"
	default:
		return ""
	}
}

func tabViewMode(current string, key string, order []string) string {
	if (key != "tab" && key != "btab") || len(order) == 0 {
		return ""
	}
	step := 1
	if key == "btab" {
		step = -1
	}
	for i, mode := range order {
		if mode == current {
			return order[(i+step+len(order))%len(order)]
		}
	}
	if key == "btab" {
		return order[len(order)-1]
	}
	return order[0]
}

func validViewMode(mode string) bool {
	switch mode {
	case "palette", "agents", "tools", "projects", "links", "bookmarks", "status":
		return true
	default:
		return false
	}
}

func runPickerLoop(ctx context.Context, mode string) error {
	for {
		result, err := selectMode(ctx, mode)
		if errors.Is(err, picker.ErrCanceled) {
			return nil
		}
		if err != nil {
			return err
		}
		if next := viewModeForKey(result.Key); next != "" {
			mode = next
			continue
		}
		if result.Key == "tab" || result.Key == "btab" {
			cfg, _, err := loadConfig(ctx)
			if err != nil {
				return err
			}
			mode = tabViewMode(mode, result.Key, cfg.Picker.TabOrder)
			continue
		}
		if !result.Selected {
			return nil
		}
		return dispatch(ctx, dispatchForResult(result))
	}
}

func runStatusCommand(ctx context.Context, command string, rt runtimeContext) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"TMUX_MENU_ORIGIN_PANE="+rt.OriginPane,
		"TMUX_MENU_ORIGIN_PATH="+rt.OriginPath,
		"TMUX_MENU_SESSION_ID="+rt.SessionID,
		"TMUX_MENU_SESSION_NAME="+rt.SessionName,
		"TMUX_MENU_SESSION_PATH="+rt.SessionPath,
	)
	if rt.SessionPath != "" {
		cmd.Dir = rt.SessionPath
	}
	return cmd.Run()
}

func dispatchForResult(result picker.Result[menuItem]) action.Dispatch {
	item := result.Value
	if result.Key != "" && result.Key == item.alternateKey && item.alternateDispatch.Mode != "" {
		return item.alternateDispatch
	}
	return item.dispatch
}

func selectMode(ctx context.Context, mode string) (picker.Result[menuItem], error) {
	switch mode {
	case "palette":
		return selectPalette(ctx)
	case "agents":
		return selectAgents(ctx)
	case "tools":
		return selectTools(ctx)
	case "projects":
		return selectProjects(ctx)
	case "links":
		return selectLinks(ctx)
	case "bookmarks":
		return selectBookmarks(ctx)
	case "status":
		return selectStatus(ctx)
	default:
		return picker.Result[menuItem]{}, fmt.Errorf("unknown mode %q", mode)
	}
}
