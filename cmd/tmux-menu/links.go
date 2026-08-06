package main

import (
	"context"
	"fmt"
	"os"
	"strings"

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
	scrollback, err := tmux.CapturePane(ctx, rt.OriginPane, linkHistoryLines)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	found := linkscan.Extract(scrollback, rt.OriginPath, cfg.Links.URLSchemes)
	items := make([]picker.Item[menuItem], 0, len(found))
	for _, item := range found {
		items = append(items, picker.Item[menuItem]{
			Label: linkLabel(item),
			Value: linkMenuItem(item, rt.OriginPath, cfg),
		})
	}
	return picker.SelectWithExpect(ctx, "links> ", items, linkExpectKeys(cfg.Links.Alternate), linkFooterForConfig(cfg))
}

func linkMenuItem(item linkscan.Item, workingDir string, cfg config.Config) menuItem {
	return menuItem{
		dispatch:          linkDispatch(item, workingDir, cfg.Editor, cfg.Links.Open),
		alternateKey:      cfg.Links.Alternate.Key,
		alternateDispatch: linkAlternateDispatch(item, cfg.Links.Alternate),
	}
}

func linkExpectKeys(alt config.LinkAlternateConfig) []string {
	keys := append([]string(nil), viewSwitchKeys...)
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

func linkFooterForConfig(cfg config.Config) string {
	footer := viewSwitchFooter()
	if !cfg.Picker.ShowHelp || strings.TrimSpace(cfg.Links.Alternate.Key) == "" {
		return footer
	}
	altHelp := cfg.Links.Alternate.Key + " alternate open"
	if footer == "" {
		return altHelp
	}
	return altHelp + "\n" + footer
}

func linkLabel(item linkscan.Item) string {
	switch item.Kind {
	case linkscan.KindURL:
		return fmt.Sprintf("%s      %s  %s", colorKind("url"), item.Target, dim(truncate(item.SourceLine, 90)))
	case linkscan.KindFile:
		target := shortenHome(item.Target)
		if item.Line > 0 {
			target += fmt.Sprintf(":%d", item.Line)
			if item.Column > 0 {
				target += fmt.Sprintf(":%d", item.Column)
			}
		}
		return fmt.Sprintf("%s     %s  %s", colorKind("file"), target, dim(truncate(item.SourceLine, 90)))
	default:
		return fmt.Sprintf("%s     %s", colorKind("link"), item.Target)
	}
}

func linkDispatch(item linkscan.Item, workingDir string, editor config.EditorConfig, openConfig config.OpenConfig) action.Dispatch {
	var open action.Dispatch
	switch item.Kind {
	case linkscan.KindURL:
		open = action.Dispatch{
			Mode: "shell",
			Cmd:  "open " + shellquote.Quote(item.Target),
		}
	case linkscan.KindFile:
		open = fileOpenDispatch(item, workingDir, editor, openConfig)
	default:
		return action.Dispatch{}
	}
	return action.Dispatch{
		Mode: "sequence",
		Steps: []action.Dispatch{
			linkClipboardDispatch(item.Target),
			open,
		},
	}
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

func linkAlternateDispatch(item linkscan.Item, alt config.LinkAlternateConfig) action.Dispatch {
	var command string
	switch item.Kind {
	case linkscan.KindURL:
		command = alt.URLCommand
	case linkscan.KindFile:
		command = alt.FileCommand
	default:
		return action.Dispatch{}
	}
	command = commandWithTarget(command, item.Target)
	if command == "" {
		return action.Dispatch{}
	}
	return action.Dispatch{
		Mode: "sequence",
		Steps: []action.Dispatch{
			linkClipboardDispatch(item.Target),
			{Mode: "shell", Cmd: command},
		},
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
	return action.Dispatch{
		Mode: "shell",
		Cmd:  "printf %s " + shellquote.Quote(target) + " | pbcopy",
	}
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
		if item.Column > 0 {
			args = append(args, shellquote.Quote(fmt.Sprintf("+call cursor(%d,%d)", item.Line, item.Column)))
		} else {
			args = append(args, fmt.Sprintf("+%d", item.Line))
		}
	}
	args = append(args, shellquote.Quote(item.Target))
	return strings.Join(args, " ")
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
