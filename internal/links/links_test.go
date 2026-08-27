package links

import (
	"os"
	"path/filepath"
	"testing"
)

var defaultURLSchemes = []string{"http", "https", "slack", "tg"}

func TestExtractURLsAndExistingFileRefs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "cmd", "main.go"))
	mustWrite(t, filepath.Join(dir, "README.md"))

	scrollback := `see https://example.com/a/b?x=1.
panic at cmd/main.go:12:3:
open ./README.md:5 for notes
ignore missing.go:9`

	items := Extract(scrollback, dir, defaultURLSchemes, "")
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

func TestExtractUsesConfiguredURLSchemes(t *testing.T) {
	scrollback := `web https://example.com
Slack slack://channel?team=T123&id=C456.
Telegram TG://resolve?domain=example)
email mailto:user@example.com
ignored ftp://example.com`

	items := Extract(scrollback, "", []string{"slack", "tg", "mailto"}, "")
	if len(items) != 3 {
		t.Fatalf("expected 3 configured URLs, got %#v", items)
	}
	for i, want := range []string{
		"slack://channel?team=T123&id=C456",
		"TG://resolve?domain=example",
		"mailto:user@example.com",
	} {
		if items[i].Kind != KindURL || items[i].Target != want {
			t.Fatalf("item %d = %#v, want URL %q", i, items[i], want)
		}
	}
}

func TestExtractDoesNotMatchSchemeInsideAnotherScheme(t *testing.T) {
	items := Extract("fooslack://channel?team=T123&id=C456", "", []string{"slack"}, "")
	if len(items) != 0 {
		t.Fatalf("expected no partial scheme match, got %#v", items)
	}
}

func TestExtractDeduplicatesFileRefs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"))

	items := Extract("main.go:1\nmain.go:1\nmain.go:2", dir, defaultURLSchemes, "")
	if len(items) != 2 {
		t.Fatalf("expected 2 unique file refs, got %#v", items)
	}
}

func TestExtractFileRangeUsesStartLine(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "internal", "config", "config.go"))

	items := Extract("see internal/config/config.go:33-55", dir, defaultURLSchemes, "")
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

	items := Extract("see internal/config/config.go:13 - config", dir, defaultURLSchemes, "")
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

	items := Extract("Saved the plan to docs/livekit-room-chat-plan.md. I also linked it.", dir, defaultURLSchemes, "")
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

	items := Extract("linked from root README.md:20.", dir, defaultURLSchemes, "")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %#v", items)
	}
	if items[0].Line != 20 {
		t.Fatalf("line = %d, want 20", items[0].Line)
	}
}

func TestExtractJiraIssueKeysAsURLs(t *testing.T) {
	scrollback := `work on INF-220 and VAPC-1234.
repeat INF-220
ignore lowercase inf-221 and embedded prefixINF-222`

	items := Extract(scrollback, "", defaultURLSchemes, "https://dentiai.atlassian.net")
	if len(items) != 2 {
		t.Fatalf("expected 2 Jira URLs, got %#v", items)
	}
	for i, want := range []struct {
		raw    string
		target string
	}{
		{raw: "INF-220", target: "https://dentiai.atlassian.net/browse/INF-220"},
		{raw: "VAPC-1234", target: "https://dentiai.atlassian.net/browse/VAPC-1234"},
	} {
		if items[i].Kind != KindJira || items[i].Raw != want.raw || items[i].Target != want.target {
			t.Fatalf("item %d = %#v, want Jira URL %#v", i, items[i], want)
		}
	}
}

func TestExtractCanonicalizesJiraURLWithEncodedBacktick(t *testing.T) {
	items := Extract(
		"https://dentiai.atlassian.net/browse/INF-234%60",
		"",
		defaultURLSchemes,
		"https://dentiai.atlassian.net",
	)
	if len(items) != 1 {
		t.Fatalf("expected one canonical Jira URL, got %#v", items)
	}
	if items[0].Kind != KindJira ||
		items[0].Raw != "INF-234" ||
		items[0].Target != "https://dentiai.atlassian.net/browse/INF-234" {
		t.Fatalf("unexpected Jira item: %#v", items[0])
	}
}

func TestExtractIgnoresJiraIssueKeysWithoutBaseURL(t *testing.T) {
	items := Extract("work on INF-220", "", defaultURLSchemes, "")
	if len(items) != 0 {
		t.Fatalf("expected no Jira URLs without a configured base URL, got %#v", items)
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
