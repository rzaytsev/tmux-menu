package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsOverlayKeepsOtherDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[docs]\ndirs = ['./runbooks']\nignore_patterns = []\n[docs.open]\npane_side = 'below'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Docs.Dirs, ",") != "./runbooks" || len(cfg.Docs.IgnorePatterns) != 0 || cfg.Docs.PreviewCommand != "glow" || cfg.Docs.Open.Mode != "pane" || cfg.Docs.Open.PaneSide != "below" {
		t.Fatalf("docs overlay = %#v", cfg.Docs)
	}
}

func TestPreviewTemplatesAndWorkflowKeysValidated(t *testing.T) {
	for _, key := range []string{"ctrl-y", "ctrl-r", "alt-7", "alt-8"} {
		cfg := Default()
		cfg.Links.Alternate.Key = key
		if err := Validate(cfg); err == nil {
			t.Fatalf("accepted reserved key %q", key)
		}
	}
	for _, view := range []string{"docs", "status"} {
		cfg := Default()
		if view == "docs" {
			cfg.Docs.PreviewCommand = "cat '{}'"
		} else {
			cfg.Status.PreviewCommand = "cat '{}'"
		}
		if err := Validate(cfg); err == nil {
			t.Fatalf("accepted single-quoted %s placeholder", view)
		}
	}
}
