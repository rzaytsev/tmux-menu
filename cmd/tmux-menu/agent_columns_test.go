package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"tmux-menu/internal/config"
	"tmux-menu/internal/tmux"
)

func TestAgentColumnsAlignColoredUnicodeAndTruncateLongFields(t *testing.T) {
	rows := []agentRow{
		{pane: tmux.Pane{SessionName: "开发", PaneTitle: "Fix issue", CurrentPath: "/tmp/one", PaneID: "%1"}, status: agentStatusWorking, name: "codex"},
		{pane: tmux.Pane{SessionName: "a-very-long-session-name", PaneTitle: strings.Repeat("界", 90), CurrentPath: "/tmp/two", PaneID: "%2"}, status: agentStatusAttention, name: "claude"},
	}
	for _, columns := range []int{40, 80, 120, 200} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			items, header := agentPickerItemsForRows(rows, "%1", nil, config.Default().Agents, columns)
			for i, item := range items {
				if ansi.StringWidth(item.Label) > columns-4 || strings.ContainsAny(item.Label, "\n\t") {
					t.Fatalf("row overflows %d columns: %q", columns, item.Label)
				}
				if item.Preview != rows[i].pane.PaneID || item.Value.dispatch.PaneID != rows[i].pane.PaneID {
					t.Fatal("formatting changed the pane target")
				}
				if columns >= 80 {
					plain := stripANSI(item.Label)
					path := rows[i].pane.CurrentPath
					prefix, _, found := strings.Cut(plain, path)
					headerPrefix, _, _ := strings.Cut(stripANSI(header), "WORKDIR")
					if !found || ansi.StringWidth(prefix) != ansi.StringWidth(headerPrefix) {
						t.Fatalf("workdir misaligned: %q under %q", plain, stripANSI(header))
					}
				}
			}
			if columns <= 120 && !strings.Contains(items[1].Label, "…") {
				t.Fatal("long title must truncate")
			}
		})
	}
}

func TestAutomaticSessionColorsRespectConfigAndStayStable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	panes := []tmux.Pane{{SessionID: "$1", SessionName: "work", SessionPath: root}, {SessionID: "$2", SessionName: "docs", SessionPath: root}}
	seen := map[string]bool{}
	for i := range 30 {
		p := tmux.Pane{SessionName: fmt.Sprintf("session-%d", i)}
		seen[sessionColorForPane(p, nil)] = true
	}
	if len(seen) < 3 {
		t.Fatal("automatic colors should vary across sessions")
	}
	check := func(want string) {
		t.Helper()
		colors, err := loadAgentSessionColors(panes, config.DefaultSessionColor)
		if err != nil {
			t.Fatal(err)
		}
		bounded, err := loadAgentSessionColorsBounded(context.Background(), panes, config.DefaultSessionColor, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, pane := range panes {
			got := sessionColorForPane(pane, colors)
			if got != sessionColorForPane(pane, bounded) || want != "auto" && got != want {
				t.Fatalf("color = %q, wanted %q; bounded = %#v", got, want, bounded)
			}
			cfg := config.Default()
			cfg.Session.Color = got
			if want == "auto" && (got != sessionColorForPane(pane, nil) || got == "auto" || config.Validate(cfg) != nil) {
				t.Fatalf("unstable or invalid automatic color %q", got)
			}
		}
	}
	check("auto")
	if err := os.WriteFile(config.DefaultPath(), []byte("[session]\ncolor = 'red'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check("red")
	if err := os.WriteFile(filepath.Join(root, ".tmux-menu.conf"), []byte("[session]\ncolor = 'default'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check("default")
	if err := os.WriteFile(filepath.Join(root, ".tmux-menu.conf"), []byte("[session]\ncolor = 'auto'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check("auto")
}
