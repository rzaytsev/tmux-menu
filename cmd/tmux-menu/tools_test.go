package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tmux-menu/internal/action"
	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
	"tmux-menu/internal/shellquote"
)

func TestRunNamedToolDispatch(t *testing.T) {
	for _, mode := range []string{"popup", "paste", "window", "tmux", "shell"} {
		t.Run(mode, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			origin := filepath.Join(root, "subdir")
			if err := os.Mkdir(origin, 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("TMUX_MENU_ORIGIN_PANE", "%7")
			t.Setenv("TMUX_MENU_ORIGIN_PATH", origin)
			t.Setenv("TMUX_MENU_SESSION_ID", "$4")
			t.Setenv("TMUX_MENU_SESSION_NAME", "project")
			t.Setenv("TMUX_MENU_SESSION_PATH", root)
			dispatchPath := filepath.Join(t.TempDir(), "dispatch.json")
			t.Setenv("TMUX_MENU_DISPATCH_FILE", dispatchPath)
			write := func(dir, text string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, ".tmux-menu.conf"), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(home, `[[commands]]
title = "dash"
session = "other"
mode = "shell"
cmd = "exit 99"
`)
			write(root, fmt.Sprintf(`[[commands]]
title = "dash"
session = "project"
mode = %q
cmd = "printf dashboard"
enter = true
window_name = "dashboard"
`, mode))
			write(origin, "[popup]\nwidth = \"70%\"\nheight = \"60%\"\n")
			if err := run(context.Background(), []string{"tools", "dash"}); err != nil {
				t.Fatal(err)
			}
			d, err := action.Read(dispatchPath)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadForContext(origin, root)
			if err != nil {
				t.Fatal(err)
			}
			want := toolsItems(cfg, origin, "project", root)[0].Value.dispatch
			if !reflect.DeepEqual(d, want) {
				t.Fatalf("direct dispatch = %#v, picker dispatch = %#v", d, want)
			}
			for _, tc := range []struct {
				name string
				want string
			}{
				{"missing", "no configured command"},
				{"Dash", "no configured command"},
			} {
				if err := run(context.Background(), []string{"tools", tc.name}); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("tools %q error = %v, want %q", tc.name, err, tc.want)
				}
			}
			write(origin, "[[commands]]\ntitle = \"dash\"\nmode = \"shell\"\ncmd = \"exit 98\"\n")
			if err := os.Remove(dispatchPath); err != nil {
				t.Fatal(err)
			}
			if err := run(context.Background(), []string{"tools", "dash"}); err == nil || !strings.Contains(err.Error(), "multiple configured commands") {
				t.Fatalf("duplicate command error = %v", err)
			}
			if _, err := os.Stat(dispatchPath); !os.IsNotExist(err) {
				t.Fatalf("ambiguous command dispatched: %v", err)
			}
		})
	}
}

func TestRunNamedToolExecutesAndReturnsFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("TMUX_MENU_DISPATCH_FILE", "")
	t.Setenv("TMUX_MENU_ORIGIN_PANE", "%7")
	t.Setenv("TMUX_MENU_ORIGIN_PATH", root)
	t.Setenv("TMUX_MENU_SESSION_ID", "$4")
	t.Setenv("TMUX_MENU_SESSION_NAME", "project")
	t.Setenv("TMUX_MENU_SESSION_PATH", root)
	marker := filepath.Join(root, "ran")
	text := fmt.Sprintf("[[commands]]\ntitle = \"dash\"\nmode = \"shell\"\ncmd = %q\n", "printf dashboard > "+shellquote.Quote(marker))
	text += "[[commands]]\ntitle = \"fail\"\nmode = \"shell\"\ncmd = \"exit 23\"\n"
	if err := os.WriteFile(filepath.Join(root, ".tmux-menu.conf"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"tools", "dash"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "dashboard" {
		t.Fatalf("command output = %q, error = %v", got, err)
	}
	if err := run(context.Background(), []string{"tools", "fail"}); err == nil || !strings.Contains(err.Error(), "exit status 23") {
		t.Fatalf("command failure = %v", err)
	}
}

func TestRunToolsWithoutNameKeepsPicker(t *testing.T) {
	old := selectModeForLoop
	t.Cleanup(func() { selectModeForLoop = old })
	called := false
	selectModeForLoop = func(_ context.Context, mode, _ string) (picker.Result[menuItem], error) {
		called = true
		if mode != "tools" {
			t.Fatalf("picker mode = %q", mode)
		}
		return picker.Result[menuItem]{}, picker.ErrCanceled
	}
	if err := run(context.Background(), []string{"tools"}); err != nil || !called {
		t.Fatalf("picker called = %v, error = %v", called, err)
	}
}

func TestRunToolsRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"tools", "dash", "extra"}, {"tools", ""}, {"tools", " "}} {
		if err := run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "usage: tmux-menu tools [name]") {
			t.Fatalf("run(%q) error = %v", args, err)
		}
	}
}
