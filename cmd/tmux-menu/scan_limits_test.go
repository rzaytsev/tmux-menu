package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"tmux-menu/internal/config"
)

func TestDiscoverySkipsOversizedFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"large.md", "Makefile"} {
		f, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(32 << 20); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	items, err := extractBookmarksFromFile(filepath.Join(root, "large.md"), "test")
	if err != nil || len(items) != 0 || len(makefileTargetsInDir(root)) != 0 {
		t.Fatalf("oversized discovery was not skipped: %#v, %v", items, err)
	}
}

func TestBookmarksReadOverlappingFilesOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("[docs](https://example.com/docs)"), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := collectBookmarks(config.BookmarksConfig{Dirs: []string{root, root}}, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("overlap = %#v, %v", items, err)
	}
}

func TestTaskMetadataReadsOnlyPrefixAndPrunesIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	lane := filepath.Join(root, "doing")
	ignored := filepath.Join(lane, "ignored")
	if err := os.MkdirAll(ignored, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "task.md"), []byte(strings.Repeat("body\n", 15000)+"summary: too late\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored, "hidden.md"), []byte("summary: hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := listStatusFiles(root, []string{"doing"}, []string{"ignored"})
	if err != nil || len(items) != 1 || items[0].Summary != "(no summary)" {
		t.Fatalf("task scan = %#v, %v", items, err)
	}
}

func TestMakeTargetsCannotBecomeOptions(t *testing.T) {
	for _, target := range []string{"-f", "--eval", "--file/path"} {
		if _, ok := parseMakeTargetLine(target + ":"); ok {
			t.Fatalf("option-like target accepted: %q", target)
		}
	}
}

func TestUnicodeTruncationKeepsValidText(t *testing.T) {
	if got := truncateStatusText("éééééééééé", 7); !utf8.ValidString(got) || got != "éééé..." {
		t.Fatalf("truncation = %q", got)
	}
}

func TestLegacyStatusMockUsesPrivateScratch(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "keep.md")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	plantedDir := filepath.Join(root, "tmux-menu-status-new-demo", "todo", "new")
	if err := os.MkdirAll(plantedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(plantedDir, "01-shape-status-board.md")); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(root, "fzf")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\ncat > \"$MOCK_ROWS\"\nprintf '%s\\n' \"$@\" > \"$MOCK_ARGS\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rowsPath, argsPath := filepath.Join(root, "rows"), filepath.Join(root, "args")
	cmd := exec.Command("/bin/sh", "../../scripts/status-new-mock.sh")
	cmd.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"), "TMPDIR="+root,
		"STATUS_SESSION_ROOT="+root, "STATUS_TODO_ROOT="+filepath.Join(root, "missing"),
		"MOCK_ROWS="+rowsPath, "MOCK_ARGS="+argsPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mock: %v: %s", err, out)
	}
	contents, err := os.ReadFile(victim)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("predictable demo path followed planted symlink: %q, %v", contents, err)
	}
	rows, err := os.ReadFile(rowsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range strings.Split(strings.TrimSpace(string(rows)), "\n") {
		path, _, _ := strings.Cut(row, "\t")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("demo scratch was not cleaned: %q", path)
		}
	}
	args, err := os.ReadFile(argsPath)
	if err != nil || !strings.Contains(string(args), `sh "$TMUX_MENU_STATUS_MOCK_SCRIPT" --preview {1}`) {
		t.Fatalf("unsafe preview template: %q, %v", args, err)
	}
}
