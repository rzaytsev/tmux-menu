package links

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractURLsAndExistingFileRefs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "cmd", "main.go"))
	mustWrite(t, filepath.Join(dir, "README.md"))

	scrollback := `see https://example.com/a/b?x=1.
panic at cmd/main.go:12:3:
open ./README.md:5 for notes
ignore missing.go:9`

	items := Extract(scrollback, dir)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %#v", items)
	}
	if items[0].Kind != KindURL || items[0].Target != "https://example.com/a/b?x=1" {
		t.Fatalf("unexpected url item: %#v", items[0])
	}
	if items[1].Kind != KindFile ||
		items[1].Target != filepath.Join(dir, "cmd", "main.go") ||
		items[1].Line != 12 ||
		items[1].Column != 3 {
		t.Fatalf("unexpected first file item: %#v", items[1])
	}
	if items[2].Kind != KindFile ||
		items[2].Target != filepath.Join(dir, "README.md") ||
		items[2].Line != 5 {
		t.Fatalf("unexpected second file item: %#v", items[2])
	}
}

func TestExtractDeduplicatesFileRefs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"))

	items := Extract("main.go:1\nmain.go:1\nmain.go:2", dir)
	if len(items) != 2 {
		t.Fatalf("expected 2 unique file refs, got %#v", items)
	}
}

func TestExtractFileRangeUsesStartLine(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "internal", "config", "config.go"))

	items := Extract("see internal/config/config.go:33-55", dir)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %#v", items)
	}
	if items[0].Line != 33 {
		t.Fatalf("line = %d, want 33", items[0].Line)
	}
	if items[0].Column != 0 {
		t.Fatalf("column = %d, want 0", items[0].Column)
	}
}

func TestExtractFileLineBeforeTrailingDash(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "internal", "config", "config.go"))

	items := Extract("see internal/config/config.go:13 - config", dir)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %#v", items)
	}
	if items[0].Line != 13 {
		t.Fatalf("line = %d, want 13", items[0].Line)
	}
}

func TestExtractFileRefBeforeTrailingPeriod(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "docs", "livekit-room-chat-plan.md"))

	items := Extract("Saved the plan to docs/livekit-room-chat-plan.md. I also linked it.", dir)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %#v", items)
	}
	if items[0].Target != filepath.Join(dir, "docs", "livekit-room-chat-plan.md") {
		t.Fatalf("target = %q", items[0].Target)
	}
}

func TestExtractFileLineBeforeTrailingPeriod(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"))

	items := Extract("linked from root README.md:20.", dir)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %#v", items)
	}
	if items[0].Line != 20 {
		t.Fatalf("line = %d, want 20", items[0].Line)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
