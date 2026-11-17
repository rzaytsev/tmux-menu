package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-menu/internal/completion"
	"tmux-menu/internal/config"
)

func TestCompletionTextKeepsRawValuesSeparateFromLabels(t *testing.T) {
	entries := []completion.Entry{{Kind: "file", Value: "path space.go", Detail: "not inserted"}, {Kind: "branch", Value: "feature"}}
	for key, want := range map[string]string{"": "path space.go feature", "ctrl-s": "'path space.go' 'feature'", "ctrl-y": "path space.go\nfeature"} {
		got, err := completionText(entries, key)
		if err != nil || got != want {
			t.Fatalf("%q: %q, %v; want %q", key, got, err, want)
		}
	}
	if _, err := completionText(entries, "alt-enter"); err == nil {
		t.Fatal("ambiguous @path accepted")
	}
	got, err := completionText([]completion.Entry{{Kind: "file", Value: "src/main.go"}, {Kind: "dir", Value: "docs"}}, "alt-enter")
	if err != nil || got != "@src/main.go @docs" {
		t.Fatalf("@ refs = %q, %v", got, err)
	}
	for _, value := range []string{"line\nbreak", "tab\tkey", string([]byte{0xff}), strings.Repeat("x", (64<<10)+1)} {
		if _, err := completionText([]completion.Entry{{Value: value}}, ""); err == nil {
			t.Fatal("unsafe or overlong insertion accepted")
		}
	}
}

func TestCompletionPreviewIsBoundedAndInert(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "$(touch sentinel).txt")
	if err := os.WriteFile(file, []byte("hello\x1b]52;c;clipboard\a\n\x1b[2Jworld\n"), 0600); err != nil {
		t.Fatal(err)
	}
	token := func(entry completion.Entry) string {
		data, _ := json.Marshal(entry)
		return base64.RawURLEncoding.EncodeToString(data)
	}
	if err := previewCompletion(token(completion.Entry{Kind: "file", Value: "literal", Path: file})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
		t.Fatal("preview executed path")
	}
	if err := previewCompletion(token(completion.Entry{Kind: "commit", Root: root, Object: "--output=sentinel"})); err == nil {
		t.Fatal("invalid revision accepted")
	}
	if err := previewCompletion(strings.Repeat("x", (64<<10)+1)); err == nil {
		t.Fatal("overlong token accepted")
	}
	safe := safeCompletionBlock("\x1b]52;c;clipboard\a\x1b[2Jtext\n" + strings.Repeat("line\n", 250))
	if strings.Contains(safe, "clipboard") || strings.Contains(safe, "\x1b[2J") || !strings.Contains(safe, "limited to 200") {
		t.Fatalf("unsafe preview: %q", safe)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if err := previewCompletion(token(completion.Entry{Kind: "file", Path: link})); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionCommandAndNavigation(t *testing.T) {
	if err := validatePopupArgs([]string{"complete", "branches"}); err != nil {
		t.Fatal(err)
	}
	if err := validatePopupArgs([]string{"complete", "bogus"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if !validViewMode("complete") || viewModeForKey("alt-9") != "complete" {
		t.Fatal("completion navigation missing")
	}
	cfg := config.Default()
	cfg.Picker.TabOrder = append(cfg.Picker.TabOrder, "complete")
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Links.Alternate.Key = "alt-9"
	if err := config.Validate(cfg); err == nil {
		t.Fatal("Completion navigation key accepted as alternate opener")
	}
}
