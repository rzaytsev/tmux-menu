package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultPathUsesHomeDotConf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := DefaultPath()
	want := filepath.Join(home, ".tmux-menu.conf")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestLoadMissingUsesDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir() + "/missing.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Palette.Sections; strings.Join(got, ",") != "sessions,panes" {
		t.Fatalf("unexpected palette sections: %#v", got)
	}
	if cfg.Picker.ShowHelp {
		t.Fatal("picker helper line should be hidden by default")
	}
	if cfg.Projects.BootstrapFile != ".tmux-sessionizer" {
		t.Fatalf("unexpected project bootstrap file: %q", cfg.Projects.BootstrapFile)
	}
	if got := strings.Join(cfg.Projects.Roots, ","); got != "~/projects" {
		t.Fatalf("unexpected project roots: %#v", cfg.Projects.Roots)
	}
	if got := strings.Join(cfg.Status.StatusDirs, ","); got != "./todo" {
		t.Fatalf("unexpected status dirs: %#v", cfg.Status.StatusDirs)
	}
	if got := strings.Join(cfg.Status.Statuses, ","); got != "new,doing,done" {
		t.Fatalf("unexpected statuses: %#v", cfg.Status.Statuses)
	}
	if cfg.Status.PreviewCommand != "glow" {
		t.Fatalf("unexpected status preview command: %q", cfg.Status.PreviewCommand)
	}
	if got := strings.Join(cfg.Status.IgnorePatterns, ","); got != ".gitkeep" {
		t.Fatalf("unexpected status ignore patterns: %#v", cfg.Status.IgnorePatterns)
	}
	if got := strings.Join(cfg.Bookmarks.Dirs, ","); got != "~/notes/projects/{project},~/projects/{project}" {
		t.Fatalf("unexpected bookmark dirs: %#v", cfg.Bookmarks.Dirs)
	}
	if got := strings.Join(cfg.Bookmarks.IgnorePatterns, ","); got != ".git,.tmp,vendor" {
		t.Fatalf("unexpected bookmark ignore patterns: %#v", cfg.Bookmarks.IgnorePatterns)
	}
	if cfg.Popup.Width != "90%" || cfg.Popup.Height != "80%" {
		t.Fatalf("unexpected main popup size: %sx%s", cfg.Popup.Width, cfg.Popup.Height)
	}
	if cfg.Editor.Command != "$EDITOR" {
		t.Fatalf("unexpected editor command: %q", cfg.Editor.Command)
	}
	if cfg.Editor.Popup.Width != "80%" || cfg.Editor.Popup.Height != "80%" || cfg.Editor.Popup.Border != "rounded" {
		t.Fatalf("unexpected editor popup: %#v", cfg.Editor.Popup)
	}
	if cfg.Links.Alternate.Key != "alt-enter" ||
		cfg.Links.Alternate.FileCommand != "open -a TextEdit" ||
		cfg.Links.Alternate.URLCommand != "open -a Safari" {
		t.Fatalf("unexpected links alternate config: %#v", cfg.Links.Alternate)
	}
	if cfg.Links.Open.Mode != "popup" || cfg.Links.Open.PaneSide != "right" {
		t.Fatalf("unexpected links open config: %#v", cfg.Links.Open)
	}
	if cfg.Bookmarks.Open.Mode != "pane" || cfg.Bookmarks.Open.PaneSide != "right" {
		t.Fatalf("unexpected bookmarks open config: %#v", cfg.Bookmarks.Open)
	}
	if cfg.Status.Open.Mode != "pane" || cfg.Status.Open.PaneSide != "below" {
		t.Fatalf("unexpected status open config: %#v", cfg.Status.Open)
	}
}

func TestLoadTomlConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(`
[palette]
sections = ["agents", "sessions", "panes"]

[picker]
show_help = true

[projects]
roots = ["~/projects", "~/code"]
bootstrap_file = ".tmux-bootstrap"

[popup]
width = "70%"
height = "60%"
border = "heavy"

[editor]
command = "vim"

[editor.popup]
width = "85%"
height = "75%"
border = "rounded"

[links.alternate]
key = "ctrl-o"
file_command = "open -a Marked {}"
url_command = "open -a Firefox {}"

[links.open]
mode = "window"
pane_side = "left"

[bookmarks]
dirs = ["~/projects/work", "~/notes"]
ignore_patterns = [".git", ".tmp", "vendor"]

[bookmarks.open]
mode = "popup"
pane_side = "above"

[status]
status_dir = ["./work-items", "./docs"]
statuses = ["backlog", "doing", "done"]
preview_command = "bat --style=plain"
ignore_patterns = [".gitkeep", "*.tmp"]

[status.open]
mode = "pane"
pane_side = "left"

[[commands]]
title = "Git status"
category = "Git"
mode = "paste"
cmd = "git status"
session = "work"
enter = true

[[quick_dirs]]
title = "docs"
path = "./docs"
command = "git status"
session = "work"
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Palette.Sections; strings.Join(got, ",") != "agents,sessions,panes" {
		t.Fatalf("palette sections = %#v", got)
	}
	if !cfg.Picker.ShowHelp {
		t.Fatal("picker.show_help should be loaded")
	}
	if cfg.Projects.BootstrapFile != ".tmux-bootstrap" {
		t.Fatalf("unexpected projects bootstrap file: %q", cfg.Projects.BootstrapFile)
	}
	if got := strings.Join(cfg.Projects.Roots, ","); got != "~/projects,~/code" {
		t.Fatalf("unexpected project roots: %#v", cfg.Projects.Roots)
	}
	if cfg.Popup.Width != "70%" || cfg.Editor.Command != "vim" {
		t.Fatalf("unexpected nested config: %#v", cfg)
	}
	if cfg.Links.Alternate.Key != "ctrl-o" ||
		cfg.Links.Alternate.FileCommand != "open -a Marked {}" ||
		cfg.Links.Alternate.URLCommand != "open -a Firefox {}" {
		t.Fatalf("unexpected links alternate config: %#v", cfg.Links.Alternate)
	}
	if cfg.Links.Open.Mode != "window" || cfg.Links.Open.PaneSide != "left" {
		t.Fatalf("unexpected links open config: %#v", cfg.Links.Open)
	}
	if cfg.Bookmarks.Open.Mode != "popup" || cfg.Bookmarks.Open.PaneSide != "above" {
		t.Fatalf("unexpected bookmarks open config: %#v", cfg.Bookmarks.Open)
	}
	if cfg.Status.Open.Mode != "pane" || cfg.Status.Open.PaneSide != "left" {
		t.Fatalf("unexpected status open config: %#v", cfg.Status.Open)
	}
	if len(cfg.Commands) != 1 || cfg.Commands[0].Cmd != "git status" || cfg.Commands[0].Session != "work" || !cfg.Commands[0].Enter {
		t.Fatalf("unexpected commands: %#v", cfg.Commands)
	}
	if len(cfg.QuickDirs) != 1 || cfg.QuickDirs[0].Session != "work" || cfg.QuickDirs[0].Command != "git status" {
		t.Fatalf("unexpected quick dirs: %#v", cfg.QuickDirs)
	}
	if strings.Join(cfg.Bookmarks.Dirs, ",") != "~/projects/work,~/notes" ||
		strings.Join(cfg.Bookmarks.IgnorePatterns, ",") != ".git,.tmp,vendor" {
		t.Fatalf("unexpected bookmarks config: %#v", cfg.Bookmarks)
	}
	if strings.Join(cfg.Status.StatusDirs, ",") != "./work-items,./docs" || strings.Join(cfg.Status.Statuses, ",") != "backlog,doing,done" ||
		cfg.Status.PreviewCommand != "bat --style=plain" ||
		strings.Join(cfg.Status.IgnorePatterns, ",") != ".gitkeep,*.tmp" {
		t.Fatalf("unexpected status config: %#v", cfg.Status)
	}
}

func TestLoadForContextOverlaysHomeSessionAndCurrentConfigs(t *testing.T) {
	home := t.TempDir()
	sessionRoot := filepath.Join(t.TempDir(), "project")
	currentDir := filepath.Join(sessionRoot, "subdir")
	for _, dir := range []string{home, sessionRoot, currentDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)

	if err := os.WriteFile(filepath.Join(home, ".tmux-menu.conf"), []byte(`
[status]
status_dir = ["./todo"]
preview_command = "glow"

[bookmarks]
dirs = ["~/notes"]

[[commands]]
title = "Global"
mode = "paste"
cmd = "echo global"
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionRoot, ".tmux-menu.conf"), []byte(`
[status]
status_dir = ["./session-todo", "./docs"]
statuses = ["backlog", "doing"]
ignore_patterns = [".gitkeep", "*.tmp"]

[bookmarks]
dirs = ["~/projects/work", "~/notes"]
ignore_patterns = [".git", ".tmp", "vendor"]

[[quick_dirs]]
title = "session docs"
path = "./docs"
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(currentDir, ".tmux-menu.conf"), []byte(`
[status]
preview_command = "bat --style=plain"

[[commands]]
title = "Local"
mode = "paste"
cmd = "echo local"
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForContext(currentDir, sessionRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Status.StatusDirs, ","); got != "./session-todo,./docs" {
		t.Fatalf("status dirs = %#v", cfg.Status.StatusDirs)
	}
	if got := strings.Join(cfg.Status.Statuses, ","); got != "backlog,doing" {
		t.Fatalf("statuses = %#v", cfg.Status.Statuses)
	}
	if cfg.Status.PreviewCommand != "bat --style=plain" {
		t.Fatalf("preview command = %q", cfg.Status.PreviewCommand)
	}
	if got := strings.Join(cfg.Status.IgnorePatterns, ","); got != ".gitkeep,*.tmp" {
		t.Fatalf("ignore patterns = %#v", cfg.Status.IgnorePatterns)
	}
	if got := strings.Join(cfg.Bookmarks.Dirs, ","); got != "~/projects/work,~/notes" {
		t.Fatalf("bookmark dirs = %#v", cfg.Bookmarks.Dirs)
	}
	if got := strings.Join(cfg.Bookmarks.IgnorePatterns, ","); got != ".git,.tmp,vendor" {
		t.Fatalf("bookmark ignore patterns = %#v", cfg.Bookmarks.IgnorePatterns)
	}
	if len(cfg.Commands) != 2 || cfg.Commands[0].Title != "Global" || cfg.Commands[1].Title != "Local" {
		t.Fatalf("commands should append across config layers: %#v", cfg.Commands)
	}
	if len(cfg.QuickDirs) != 1 || cfg.QuickDirs[0].Title != "session docs" {
		t.Fatalf("quick dirs = %#v", cfg.QuickDirs)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "examples", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Commands) == 0 {
		t.Fatal("expected example commands")
	}
	if got := cfg.Palette.Sections; strings.Join(got, ",") != "sessions,panes" {
		t.Fatalf("unexpected example palette sections: %#v", got)
	}
}

func TestExampleConfigKeepsDocumentedDefaultsInSync(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "examples", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	def := Default()
	if !reflect.DeepEqual(cfg.Palette, def.Palette) {
		t.Fatalf("example palette = %#v, want %#v", cfg.Palette, def.Palette)
	}
	if !reflect.DeepEqual(cfg.Picker, def.Picker) {
		t.Fatalf("example picker = %#v, want %#v", cfg.Picker, def.Picker)
	}
	if !reflect.DeepEqual(cfg.Projects, def.Projects) {
		t.Fatalf("example projects = %#v, want %#v", cfg.Projects, def.Projects)
	}
	if !reflect.DeepEqual(cfg.Popup, def.Popup) {
		t.Fatalf("example popup = %#v, want %#v", cfg.Popup, def.Popup)
	}
	if !reflect.DeepEqual(cfg.Editor, def.Editor) {
		t.Fatalf("example editor = %#v, want %#v", cfg.Editor, def.Editor)
	}
	if !reflect.DeepEqual(cfg.Links, def.Links) {
		t.Fatalf("example links = %#v, want %#v", cfg.Links, def.Links)
	}
	if !reflect.DeepEqual(cfg.Bookmarks, def.Bookmarks) {
		t.Fatalf("example bookmarks = %#v, want %#v", cfg.Bookmarks, def.Bookmarks)
	}
	if !reflect.DeepEqual(cfg.Status, def.Status) {
		t.Fatalf("example status = %#v, want %#v", cfg.Status, def.Status)
	}
}

func TestValidateRejectsUnknownMode(t *testing.T) {
	cfg := Default()
	cfg.Commands = []Command{{Title: "Bad", Cmd: "echo bad", Mode: "later"}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateRejectsBadPaletteSection(t *testing.T) {
	cfg := Default()
	cfg.Palette.Sections = []string{"sessions", "windows"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSampleConfigIsToml(t *testing.T) {
	got := Sample()
	for _, want := range []string{
		"[palette]",
		`sections = ["sessions", "panes"]`,
		"[picker]",
		"show_help = false",
		"[projects]",
		`roots = ["~/projects"]`,
		`bootstrap_file = ".tmux-sessionizer"`,
		"[links.alternate]",
		`key = "alt-enter"`,
		`file_command = "open -a TextEdit"`,
		`url_command = "open -a Safari"`,
		"[links.open]",
		`mode = "popup"`,
		"[bookmarks.open]",
		`pane_side = "right"`,
		"[status.open]",
		`pane_side = "below"`,
		"[[commands]]",
		"[[quick_dirs]]",
		`# command = "git status"`,
		"[bookmarks]",
		`dirs = ["~/notes/projects/{project}", "~/projects/{project}"]`,
		`ignore_patterns = [".git", ".tmp", "vendor"]`,
		"[status]",
		`status_dir = ["./todo"]`,
		`statuses = ["new", "doing", "done"]`,
		`preview_command = "glow"`,
		`ignore_patterns = [".gitkeep"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("sample config missing %q:\n%s", want, got)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("sample config should be TOML, got JSON:\n%s", got)
	}
}

func TestValidateRejectsBadQuickDir(t *testing.T) {
	cfg := Default()
	cfg.QuickDirs = []QuickDir{{Title: "missing path"}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateRejectsBadOpenConfig(t *testing.T) {
	cfg := Default()
	cfg.Links.Open.Mode = "tab"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected bad open mode validation error")
	}

	cfg = Default()
	cfg.Status.Open.PaneSide = "middle"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected bad pane side validation error")
	}
}

func TestValidateRejectsReservedAlternateKeys(t *testing.T) {
	for _, key := range []string{"enter", "alt-1", "alt-2", "alt-3", "alt-4", "alt-5", "alt-6", "ctrl-o,alt-o", "ctrl o"} {
		t.Run(key, func(t *testing.T) {
			cfg := Default()
			cfg.Links.Alternate.Key = key
			if err := Validate(cfg); err == nil {
				t.Fatalf("expected reserved key %q to fail validation", key)
			}
		})
	}
}

func TestValidateRejectsSingleQuotedAlternatePlaceholder(t *testing.T) {
	cfg := Default()
	cfg.Links.Alternate.FileCommand = "open '{}'"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected single-quoted placeholder validation error")
	}

	cfg.Links.Alternate.FileCommand = `open "{}"`
	if err := Validate(cfg); err != nil {
		t.Fatalf("double-quoted placeholder should be valid: %v", err)
	}
}

func TestLoadNormalizesOpenConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[links.alternate]
key = " alt-enter "

[links.open]
mode = " pane "
pane_side = " left "

[bookmarks.open]
mode = " window "
pane_side = " above "

[status.open]
mode = " popup "
pane_side = " below "
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Links.Alternate.Key != "alt-enter" {
		t.Fatalf("alternate key = %q", cfg.Links.Alternate.Key)
	}
	if cfg.Links.Open != (OpenConfig{Mode: "pane", PaneSide: "left"}) {
		t.Fatalf("links open = %#v", cfg.Links.Open)
	}
	if cfg.Bookmarks.Open != (OpenConfig{Mode: "window", PaneSide: "above"}) {
		t.Fatalf("bookmarks open = %#v", cfg.Bookmarks.Open)
	}
	if cfg.Status.Open != (OpenConfig{Mode: "popup", PaneSide: "below"}) {
		t.Fatalf("status open = %#v", cfg.Status.Open)
	}
}
