package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	linkscan "tmux-menu/internal/links"
	"tmux-menu/internal/picker"
)

func TestTypedColumnsUnicodeAndFilters(t *testing.T) {
	for _, bookmark := range []bool{false, true} {
		for _, title := range []string{"你好 world", strings.Repeat("界", 100), "e\u0301moji 👩‍💻"} {
			item := linkscan.Item{Kind: "pr", Title: title, Target: "https://github.com/org/repo/pull/1?x=()"}
			label := typedLinkLabel(item, bookmark, 100)
			parts := strings.Split(label, " | ")
			if ansi.StringWidth(parts[0]) != 7 || (bookmark && ansi.StringWidth(parts[1]) != 33) {
				t.Fatalf("unaligned %q", label)
			}
			if !strings.HasSuffix(label, item.Target) {
				t.Fatal("lost full target")
			}
		}
	}
	for _, target := range []string{"https://github.com/o/r", "https://github.com/o/r/issues/1", "https://github.com/o/r/actions/runs/1", "https://github.com?tab=1"} {
		if !typedLinkMatches(linkscan.Item{Target: target, Title: "innocent"}, "alt-g") {
			t.Fatal(target)
		}
	}
	if typedLinkMatches(linkscan.Item{Target: "https://example.com", Title: "https://github.com/o/r"}, "alt-g") {
		t.Fatal("matched title")
	}
	if typedLinkMatches(linkscan.Item{Target: "https://github.com.evil.test/o/r"}, "alt-g") {
		t.Fatal("matched lookalike")
	}
}
func TestBrowserAndEditorSafeArguments(t *testing.T) {
	root := t.TempDir()
	stub := filepath.Join(root, "open")
	out := filepath.Join(root, "args")
	mustWriteText(t, stub, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_ARGS\"\n")
	os.Chmod(stub, 0700)
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	t.Setenv("TEST_ARGS", out)
	target := "https://example.com/a(b)?x=$(touch marker)&q=';#frag"
	for _, tc := range []struct{ url, browser, prefix string }{{target, "Google Chrome", "-a\nGoogle Chrome\n--\n"}, {target, "", "--\n"}, {"slack://channel?id=one", "Firefox", "-a\nSlack\n--\n"}} {
		if err := action.Execute(context.Background(), action.Dispatch{Mode: "exec", Args: webOpenArgs(tc.url, tc.browser)}, ""); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(out)
		if string(data) != tc.prefix+tc.url+"\n" {
			t.Fatalf("argv %q", data)
		}
	}
	item := linkscan.Item{Target: root + "/x '$(touch marker).md", Line: 5, EndLine: 10}
	cmd := editorCommand(item, stub)
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if string(data) != "+normal! 10GV5G\n--\n"+item.Target+"\n" {
		t.Fatalf("argv %q", data)
	}
	if _, err := os.Stat("marker"); !os.IsNotExist(err) {
		t.Fatal("shell executed target")
	}
}
func TestBookmarksScopeOverlapAndErrors(t *testing.T) {
	root := t.TempDir()
	mustWriteText(t, filepath.Join(root, "a.md"), "[first](https://example.com)\n")
	mustWriteText(t, filepath.Join(root, "sub", "b.MD"), "[second](https://example.com)\n[local](<../space name.md#L5>)\n")
	cfg := config.BookmarksConfig{Dirs: []string{".", "sub"}, DirsBase: root}
	got, err := collectBookmarks(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("links %#v", got)
	}
	cfg.Dirs = []string{"missing"}
	if _, err := collectBookmarks(cfg, ""); err == nil {
		t.Fatal("missing directory accepted")
	}
	cfg.Dirs = []string{"a.md"}
	if _, err := collectBookmarks(cfg, ""); err == nil {
		t.Fatal("file root accepted")
	}
	cfg.Dirs = []string{}
	if got, err := collectBookmarks(cfg, ""); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
func TestMultiDispatchCopiesOnlyTargets(t *testing.T) {
	result := picker.Result[menuItem]{Key: "ctrl-y", Values: []menuItem{{copyTarget: "/a b.md:5-10"}, {copyTarget: "https://example.com?a=1&b=2"}}}
	got := dispatchForResult(result)
	want := linkClipboardDispatch("/a b.md:5-10\nhttps://example.com?a=1&b=2")
	if got.Input != want.Input || strings.Join(got.Args, ",") != strings.Join(want.Args, ",") {
		t.Fatal(got)
	}
}

func TestPopupClientSelectionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		clients        []string
		explicit, want string
	}{
		{[]string{"/dev/ttys001"}, "", "/dev/ttys001"},
		{[]string{"/dev/ttys001", "/dev/ttys002"}, "/dev/ttys001", "/dev/ttys001"},
		{[]string{"/dev/ttys001", "/dev/ttys002"}, "", ""},
		{[]string{"/dev/ttys001"}, "/dev/other", ""},
		{[]string{}, "", ""},
	} {
		got, err := choosePopupClient(tc.clients, tc.explicit)
		if got != tc.want || (tc.want == "" && err == nil) || (tc.want != "" && err != nil) {
			t.Fatal(tc, got, err)
		}
	}
}
