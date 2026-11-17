package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	"tmux-menu/internal/displaytext"
	"tmux-menu/internal/fileutil"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
	"tmux-menu/internal/tmux"
)

func typedLinkLabel(item linkscan.Item, bookmark bool, width int) string {
	cell := func(s string, w int) string {
		s = ansi.Truncate(safeLinkText(s), w, "…")
		return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
	}
	prefix := cell(string(item.Kind), 7) + " | "
	if bookmark {
		prefix += cell(item.Title, min(36, max(8, width/3))) + " | "
	}
	return prefix + safeLinkText(item.CopyTarget())
}

func selectTypedLinks(ctx context.Context, mode string, targets []linkscan.Item, cfg config.Config, rt runtimeContext) (picker.Result[menuItem], error) {
	// Freeze the source's full tmux identity before exposing any actions.
	identity, err := linkOriginIdentity(ctx, rt)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	width, _, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		width = 100
	}
	width = max(30, width-6)
	var items []picker.Item[menuItem]
	for _, target := range targets {
		d := typedLinkDispatch(target, cfg.Browser, rt, identity)
		items = append(items, picker.Item[menuItem]{Label: typedLinkLabel(target, mode == "bookmarks", width), Preview: target.CopyTarget(), Value: menuItem{dispatch: d, copyTarget: target.CopyTarget(), alternateKey: cfg.Links.Alternate.Key, alternateDispatch: linkAlternateDispatchForTyped(target, cfg.Links.Alternate)}})
	}
	exe, err := os.Executable()
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	keys := linkExpectKeys(cfg.Links.Alternate)
	filtered := keys[:0]
	for _, key := range keys {
		if key != "tab" && key != "btab" {
			filtered = append(filtered, key)
		}
	}
	keys = append(filtered, "alt-f", "alt-d", "alt-g", "alt-s", "alt-a")
	visible := items
	for {
		result, err := picker.SelectWithExpectAndPreviewOptions(ctx, mode+"> ", visible, keys, "Enter open · Tab select · Ctrl-Y copy · Ctrl-/ preview · Alt-F files / D dirs / G GitHub / S Slack / A all", shellquote.Quote(exe)+" link-preview", picker.Options{
			Multi: true, Header: typedLinkHeader(mode == "bookmarks", width), PreviewWindow: "down,40%,hidden,wrap",
			Bindings: []string{"ctrl-/:toggle-preview"},
		})
		if err != nil {
			return result, err
		}
		if !isLinkFilterKey(result.Key) {
			return result, nil
		}
		visible = nil
		for i, target := range targets {
			if typedLinkMatches(target, result.Key) {
				visible = append(visible, items[i])
			}
		}
		if state := picker.StateFromContext(ctx); state != nil {
			state.Query = ""
			state.Selected = ""
		}
	}
}

func webOpenArgs(target, browser string) []string {
	args := []string{"open"}
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "slack://") {
		args = append(args, "-a", "Slack")
	} else if browser != "" && (strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")) {
		args = append(args, "-a", browser)
	}
	return append(args, "--", target)
}

func typedLinkDispatch(item linkscan.Item, browser string, rt runtimeContext, identity action.Dispatch) action.Dispatch {
	if item.Kind != linkscan.KindFile && item.Kind != linkscan.KindDir {
		return action.Dispatch{Mode: "exec", Args: webOpenArgs(item.Target, browser)}
	}
	identity.Mode = "link-file"
	identity.TargetPath = item.Target // physical path, separately validated at dispatch
	identity.WorkingDir = rt.OriginPath
	if item.Kind == linkscan.KindDir {
		identity.Mode = "link-dir"
		return identity
	}
	identity.Cmd = editorCommand(item, "nvim")
	identity.PaneSide = "right"
	return identity
}
func linkAlternateDispatchForTyped(item linkscan.Item, alt config.LinkAlternateConfig) action.Dispatch {
	command := alt.URLCommand
	if item.Kind == linkscan.KindFile || item.Kind == linkscan.KindDir {
		command = alt.FileCommand
	}
	return action.Dispatch{Mode: "shell", Cmd: commandWithTarget(command, item.CopyTarget())}
}

func previewLink(raw string) error {
	item, err := linkscan.Resolve(raw, "", false)
	if err != nil {
		return err
	}
	fmt.Println(safeLinkText(string(item.Kind) + " | " + item.CopyTarget()))
	switch item.Kind {
	case linkscan.KindFile:
		data, err := fileutil.Read(item.Target, 1<<20)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		start := max(0, item.Line-6)
		end := min(len(lines), start+200)
		for i := start; i < end; i++ {
			fmt.Printf("%5d  %s\n", i+1, safeLinkText(lines[i]))
		}
	case linkscan.KindDir:
		f, err := os.Open(item.Target)
		if err != nil {
			return err
		}
		defer f.Close()
		entries, err := f.ReadDir(200)
		if err != nil && len(entries) == 0 {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			fmt.Println(safeLinkText(name))
		}
	default:
		fmt.Println("Web preview disabled; no network request.")
	}
	return nil
}

func safeLinkText(s string) string { return ansi.Strip(displaytext.Line(s)) }

func typedLinkMatches(item linkscan.Item, key string) bool {
	switch key {
	case "alt-f":
		return item.Kind == linkscan.KindFile
	case "alt-d":
		return item.Kind == linkscan.KindDir
	case "alt-s":
		return item.Kind == "slack"
	case "alt-g":
		u, err := url.Parse(item.Target)
		return err == nil && strings.EqualFold(u.Hostname(), "github.com") && (strings.EqualFold(u.Scheme, "https") || strings.EqualFold(u.Scheme, "http"))
	default:
		return true
	}
}
func typedLinkHeader(bookmark bool, width int) string {
	item := linkscan.Item{Kind: "type", Title: "title", Target: "target"}
	return typedLinkLabel(item, bookmark, width)
}

func isLinkFilterKey(key string) bool {
	switch key {
	case "alt-f", "alt-d", "alt-g", "alt-s", "alt-a":
		return true
	}
	return false
}
func linkOriginIdentity(ctx context.Context, rt runtimeContext) (action.Dispatch, error) {
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		return action.Dispatch{}, err
	}
	var matches []action.Dispatch
	for _, p := range panes {
		if p.PaneID == rt.OriginPane && p.SessionID == rt.SessionID {
			matches = append(matches, action.SwitchPane(p))
		}
	}
	if len(matches) != 1 {
		return action.Dispatch{}, fmt.Errorf("invoking pane is no longer live")
	}
	return matches[0], nil
}
