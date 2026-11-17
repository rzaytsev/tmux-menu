package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"tmux-menu/internal/action"
	"tmux-menu/internal/completion"
	"tmux-menu/internal/fileutil"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
	"tmux-menu/internal/tmux"
)

type completionProviderKey struct{}

func runCompleteCommand(ctx context.Context, args []string) error {
	if err := validateCompleteArgs(args); err != nil {
		return err
	}
	provider := "files"
	if len(args) == 1 {
		provider = args[0]
	}
	return runPickerLoop(context.WithValue(ctx, completionProviderKey{}, provider), "complete")
}

func validateCompleteArgs(args []string) error {
	if len(args) > 1 || len(args) == 1 && !completion.ValidProvider(args[0]) {
		return fmt.Errorf("usage: tmux-menu [popup] complete [%s]", strings.Join(completion.Providers, "|"))
	}
	return nil
}

var completionKeys = map[string]string{
	"alt-f": "files", "alt-d": "dirs", "alt-c": "changed", "alt-b": "branches",
	"alt-t": "tags", "alt-h": "commits", "alt-w": "worktrees", "alt-u": "screen",
}

func selectComplete(ctx context.Context) (picker.Result[menuItem], error) {
	cfg, rt, err := loadConfig(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	var origins []tmux.Pane
	for _, p := range panes {
		if p.PaneID == rt.OriginPane && p.SessionID == rt.SessionID {
			origins = append(origins, p)
		}
	}
	if len(origins) != 1 {
		return picker.Result[menuItem]{}, fmt.Errorf("invoking pane is no longer live")
	}
	origin := origins[0]
	identity := action.SwitchPane(origin)
	identity.Mode, identity.ForegroundCommand, identity.WorkingDir = "insert", origin.CurrentCommand, origin.CurrentPath
	provider, _ := ctx.Value(completionProviderKey{}).(string)
	if provider == "" {
		provider = "files"
	}
	exe, err := os.Executable()
	if err != nil {
		return picker.Result[menuItem]{}, err
	}
	keys := []string{"ctrl-r", "ctrl-t", "ctrl-y", "ctrl-s", "alt-enter"}
	for _, key := range viewSwitchKeys {
		if key != "tab" && key != "btab" && key != "ctrl-r" {
			keys = append(keys, key)
		}
	}
	for key := range completionKeys {
		keys = append(keys, key)
	}
	states := make(map[string]*picker.State)
	notice := ""
	for {
		if states[provider] == nil {
			states[provider] = &picker.State{}
		}
		var listing completion.Listing
		if provider == "screen" {
			listing, err = completionScreen(ctx, rt, identity, cfg.Links.URLSchemes, cfg.Links.JiraBaseURL)
		} else {
			listing, err = completion.List(ctx, origin.CurrentPath, provider)
		}
		if err != nil {
			listing = completion.Listing{Note: "Source error: " + err.Error()}
		}
		var items []picker.Item[completion.Entry]
		for _, entry := range listing.Entries {
			label := fmt.Sprintf("%-7s | %s", entry.Kind, entry.Value)
			if entry.Detail != "" {
				label += " | " + entry.Detail
			}
			payload, err := json.Marshal(entry)
			if err != nil {
				return picker.Result[menuItem]{}, err
			}
			items = append(items, picker.Item[completion.Entry]{Label: label, Preview: base64.RawURLEncoding.EncodeToString(payload), Value: entry})
		}
		header := provider + " · " + listing.Note
		if listing.Note == "" {
			header = provider
		}
		if notice != "" {
			header = notice
			notice = ""
		}
		mode, modeErr := tmux.Run(ctx, "display-message", "-p", "-t", identity.PaneID, "#{bracket_paste_flag}:#{pane_in_mode}")
		if modeErr != nil || mode != "1:0" {
			header = "paste unavailable: Ctrl-Y copies · " + header
		}
		footer := "Enter paste · Tab mark · Alt-Enter @path · Ctrl-S quote\nCtrl-Y copy · Ctrl-/ preview · Ctrl-R refresh · Ctrl-T next\nAlt: F files / D dirs / C changed / B branches\n     T tags / H commits / W worktrees / U screen"
		result, err := picker.SelectWithExpectAndPreviewOptions(picker.WithState(ctx, states[provider]), "complete> ", items, keys, footer,
			shellquote.Quote(exe)+" complete-preview", picker.Options{Multi: true, Header: header, InputHeader: []string{"cwd: " + origin.CurrentPath}, PreviewWindow: "right,50%,wrap,<60(down,40%)", Bindings: []string{"ctrl-/:toggle-preview"}})
		if err != nil {
			return picker.Result[menuItem]{}, err
		}
		if result.Key == "ctrl-r" {
			continue
		}
		if next := completionKeys[result.Key]; next != "" {
			provider = next
			continue
		}
		if result.Key == "ctrl-t" {
			for i, p := range completion.Providers {
				if p == provider {
					provider = completion.Providers[(i+1)%len(completion.Providers)]
					break
				}
			}
			continue
		}
		if viewModeForKey(result.Key) != "" {
			return picker.Result[menuItem]{Key: result.Key}, nil
		}
		if !result.Selected {
			return picker.Result[menuItem]{}, picker.ErrCanceled
		}
		entries := result.Values
		if len(entries) == 0 {
			entries = []completion.Entry{result.Value}
		}
		text, err := completionText(entries, result.Key)
		if err != nil {
			// Keep an invalid @ selection reviewable; do not silently change formats.
			notice = err.Error()
			continue
		}
		d := identity
		d.Cmd = text
		if result.Key == "ctrl-y" {
			d = linkClipboardDispatch(text)
		}
		return picker.Result[menuItem]{Selected: true, Value: menuItem{dispatch: d}}, nil
	}
}

func completionText(entries []completion.Entry, key string) (string, error) {
	var values []string
	for _, entry := range entries {
		value := entry.Value
		if !completion.SafeValue(value) {
			return "", fmt.Errorf("completion contains unsupported control characters or invalid UTF-8")
		}
		switch key {
		case "ctrl-s":
			value = shellquote.Quote(value)
		case "alt-enter":
			if (entry.Kind != "file" && entry.Kind != "dir") || strings.ContainsFunc(value, unicode.IsSpace) || strings.ContainsAny(value, "\"'`\\") {
				return "", fmt.Errorf("@path needs an unambiguous file/directory path; use Enter for literal text or Ctrl-Y to copy")
			}
			value = "@" + value
		}
		values = append(values, value)
	}
	separator := " "
	if key == "ctrl-y" {
		separator = "\n"
	}
	text := strings.Join(values, separator)
	if len(text) > 64<<10 {
		return "", fmt.Errorf("completion exceeds 64 KiB; select fewer items")
	}
	return text, nil
}

func completionScreen(ctx context.Context, rt runtimeContext, identity action.Dispatch, schemes []string, jira string) (completion.Listing, error) {
	before, err := linkOriginIdentity(ctx, rt)
	if err != nil || !sameCompletionPane(before, identity) {
		return completion.Listing{}, fmt.Errorf("completion origin changed before capture")
	}
	// Joined lines let wrapped URLs and compiler paths survive terminal width.
	out, err := tmux.RunCommandBounded(ctx, tmux.NewOutputBudget(1<<20), 1<<20, "tmux", "capture-pane", "-p", "-J", "-S", "-1000", "-t", identity.PaneID)
	if err != nil {
		return completion.Listing{}, err
	}
	after, err := linkOriginIdentity(ctx, rt)
	if err != nil || !sameCompletionPane(after, identity) {
		return completion.Listing{}, fmt.Errorf("completion origin changed during capture")
	}
	listing := completion.Listing{Note: "Screen + last 1000 scrollback lines; alternate-screen history may be unavailable"}
	for _, item := range linkscan.Extract(out, identity.WorkingDir, schemes, jira) {
		value := item.CopyTarget()
		if completion.SafeValue(value) {
			listing.Entries = append(listing.Entries, completion.Entry{Kind: string(item.Kind), Value: value, Path: item.Target, Detail: item.SourceLine})
		}
		if len(listing.Entries) == completion.MaxItems {
			listing.Note += "; row limit reached"
			break
		}
	}
	return listing, nil
}

func sameCompletionPane(a, b action.Dispatch) bool {
	return a.SessionID == b.SessionID && a.WindowID == b.WindowID && a.PaneID == b.PaneID && a.PanePID == b.PanePID
}

func previewCompletion(token string) error {
	if len(token) > 64<<10 {
		return fmt.Errorf("completion preview token too large")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return err
	}
	var entry completion.Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return err
	}
	fmt.Println(safeLinkText(entry.Kind + " | " + entry.Value))
	if entry.Object != "" {
		if !validCompletionObject(entry.Object) || !filepath.IsAbs(entry.Root) || !completion.SafeValue(entry.Root) {
			return fmt.Errorf("invalid Git preview")
		}
		out, err := completion.Git(context.Background(), entry.Root, "show", "--no-ext-diff", "--no-textconv", "--format=fuller", "--stat", "--oneline", entry.Object, "--")
		if err != nil {
			return err
		}
		fmt.Print(safeCompletionBlock(out))
		return nil
	}
	if entry.Kind == "file" || entry.Kind == "dir" {
		if !filepath.IsAbs(entry.Path) || !completion.SafeValue(entry.Path) {
			return fmt.Errorf("invalid local preview")
		}
		info, err := os.Lstat(entry.Path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("Path no longer exists (deleted or renamed). Reference can still be inserted.")
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			fmt.Println("Symlink preview disabled.")
			return nil
		}
		if info.IsDir() {
			dir, err := os.Open(entry.Path)
			if err != nil {
				return err
			}
			defer dir.Close()
			entries, err := dir.ReadDir(200)
			if err != nil && len(entries) == 0 {
				return err
			}
			for _, child := range entries {
				name := child.Name()
				if child.IsDir() {
					name += "/"
				}
				fmt.Println(safeLinkText(name))
			}
			if len(entries) == 200 {
				fmt.Println("[preview limited to 200 entries]")
			}
			return nil
		}
		data, err := fileutil.Read(entry.Path, 1<<20)
		if err != nil {
			return err
		}
		if strings.ContainsRune(string(data), 0) {
			fmt.Println("Binary file; preview omitted.")
			return nil
		}
		fmt.Print(safeCompletionBlock(string(data)))
		return nil
	}
	fmt.Println(safeLinkText(entry.Detail))
	fmt.Println("Reference only; no network preview.")
	return nil
}

func safeCompletionBlock(raw string) string {
	lines := strings.Split(raw, "\n")
	if len(lines) > 200 {
		lines = append(lines[:200], "[preview limited to 200 lines]")
	}
	for i := range lines {
		lines[i] = safeLinkText(lines[i])
	}
	return strings.Join(lines, "\n") + "\n"
}

func validCompletionObject(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
