package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-menu/internal/config"
	"tmux-menu/internal/picker"
)

func TestDocsResolveSessionRootAndDeduplicateOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	ignored := filepath.Join(root, "node_modules")
	for _, dir := range []string{docs, ignored} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(docs, "runbook.md")
	for _, file := range []string{path, filepath.Join(ignored, "readme.md")} {
		if err := os.WriteFile(file, []byte("# Restore service\nProcedure\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Docs.Dirs = []string{".", "./docs"}
	items, err := docItems(context.Background(), cfg.Docs, cfg.Editor, runtimeContext{SessionPath: root, OriginPath: t.TempDir()})
	if err != nil || len(items) != 1 {
		t.Fatalf("docs = %#v, %v", items, err)
	}
	item := items[0]
	if !strings.Contains(stripANSI(item.Label), "Restore service") || item.Preview != path || item.Value.copyTarget != path || item.Value.dispatch.WorkingDir != root || item.Value.dispatch.Mode != "pane" {
		t.Fatalf("doc lost title, raw path, or session context: %#v", item)
	}
}

func TestDocsCancellationAndTitleFallback(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "quick-start.md")
	if err := os.WriteFile(path, []byte("no heading"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := docTitle(path); got != "quick start" {
		t.Fatalf("fallback = %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := config.Default()
	cfg.Docs.Dirs = []string{root}
	_, err := docItems(ctx, cfg.Docs, cfg.Editor, runtimeContext{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestCopyOnlyDoesNotOpenTarget(t *testing.T) {
	cfg := config.Default()
	item := menuItem{dispatch: projectDispatch("/tmp/work", ""), copyTarget: "https://example.com/it's?q=$(false)"}
	got := dispatchForResult(picker.Result[menuItem]{Key: "ctrl-y", Value: item, Selected: true})
	if got.Mode != "shell" || strings.Contains(got.Cmd, "open ") || !strings.Contains(got.Cmd, "pbcopy") {
		t.Fatalf("copy-only dispatch = %#v", got)
	}
	if !containsLinkExpectKey(linkExpectKeys(cfg.Links.Alternate), "ctrl-y") {
		t.Fatal("copy shortcut not available")
	}
	if viewModeForKey("alt-7") != "links" || viewModeForKey("alt-8") != "docs" {
		t.Fatal("direct shortcuts missing")
	}
}
