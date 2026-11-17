package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
	"tmux-menu/internal/tmux"
)

const linkHistoryLines = 300

func selectLinks(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	before, err := linkOriginIdentity(ctx, rt)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	scrollback, err := tmux.Run(ctx, "capture-pane", "-p", "-J", "-S", fmt.Sprintf("-%d", linkHistoryLines), "-t", rt.OriginPane)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	after, err := linkOriginIdentity(ctx, rt)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	if before.PanePID != after.PanePID || before.WindowID != after.WindowID {
		return picker.Result[menuItem]{}, fmt.Errorf("invoking pane identity changed during capture")
	}
	found := linkscan.Extract(scrollback, rt.OriginPath, cfg.Links.URLSchemes, cfg.Links.JiraBaseURL)
	return selectTypedLinks(ctx, "links", found, cfg, rt)
}

func linkExpectKeys(alt config.LinkAlternateConfig) []string {
	keys := filePickerKeys()
	key := strings.TrimSpace(alt.Key)
	if key == "" || containsLinkExpectKey(keys, key) {
		return keys
	}
	return append(keys, key)
}

func containsLinkExpectKey(keys []string, want string) bool {
	for _, key := range keys {
		if key == want {
			return true
		}
	}
	return false
}

func fileOpenDispatch(item linkscan.Item, workingDir string, editor config.EditorConfig, openConfig config.OpenConfig) action.Dispatch {
	d := action.Dispatch{
		Mode:       openConfig.Mode,
		Cmd:        editorCommand(item, editor.Command),
		WorkingDir: workingDir,
		PaneSide:   openConfig.PaneSide,
	}
	switch d.Mode {
	case "pane":
		return d
	case "window":
		return d
	default:
		d.Mode = "popup"
		d.PopupWidth = editor.Popup.Width
		d.PopupHeight = editor.Popup.Height
		d.PopupBorder = editor.Popup.Border
		return d
	}
}

func commandWithTarget(command string, target string) string {
	command = strings.TrimSpace(os.ExpandEnv(command))
	if command == "" {
		return ""
	}
	quoted := shellquote.Quote(target)
	if strings.Contains(command, "{}") {
		script := strings.ReplaceAll(command, "{}", "$1")
		script = "set -f; IFS=; " + script
		return "sh -c " + shellquote.Quote(script) + " tmux-menu " + quoted
	}
	return command + " " + quoted
}

func linkClipboardDispatch(target string) action.Dispatch {
	return action.Dispatch{Mode: "exec", Args: []string{"pbcopy"}, Input: target}
}

func editorCommand(item linkscan.Item, command string) string {
	command = strings.TrimSpace(os.ExpandEnv(command))
	if command == "" {
		command = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if command == "" {
		command = "nvim"
	}
	args := []string{command}
	if item.Line > 0 {
		if item.EndLine > item.Line {
			args = append(args, shellquote.Quote(fmt.Sprintf("+normal! %dGV%dG", item.EndLine, item.Line)))
		} else if item.Column > 0 {
			args = append(args, shellquote.Quote(fmt.Sprintf("+call cursor(%d,%d)", item.Line, item.Column)))
		} else {
			args = append(args, fmt.Sprintf("+%d", item.Line))
		}
	}
	args = append(args, "--", shellquote.Quote(item.Target))
	return strings.Join(args, " ")
}

func truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
