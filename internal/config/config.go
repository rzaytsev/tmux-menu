package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const appName = "tmux-menu"
const configFileName = ".tmux-menu.conf"

type Config struct {
	Palette   PaletteConfig   `json:"palette" toml:"palette"`
	Picker    PickerConfig    `json:"picker" toml:"picker"`
	Projects  ProjectsConfig  `json:"projects" toml:"projects"`
	Commands  []Command       `json:"commands" toml:"commands,omitempty"`
	QuickDirs []QuickDir      `json:"quick_dirs,omitempty" toml:"quick_dirs,omitempty"`
	Popup     PopupConfig     `json:"popup" toml:"popup"`
	Editor    EditorConfig    `json:"editor" toml:"editor"`
	Links     LinksConfig     `json:"links" toml:"links"`
	Bookmarks BookmarksConfig `json:"bookmarks" toml:"bookmarks"`
	Status    StatusConfig    `json:"status" toml:"status"`
}

type PaletteConfig struct {
	Sections []string `json:"sections" toml:"sections"`
}

type PickerConfig struct {
	ShowHelp bool `json:"show_help" toml:"show_help"`
}

type ProjectsConfig struct {
	Roots         []string `json:"roots" toml:"roots"`
	BootstrapFile string   `json:"bootstrap_file" toml:"bootstrap_file"`
}

type PopupConfig struct {
	Width  string `json:"width" toml:"width"`
	Height string `json:"height" toml:"height"`
	Border string `json:"border" toml:"border"`
}

type EditorConfig struct {
	Command string      `json:"command" toml:"command"`
	Popup   PopupConfig `json:"popup" toml:"popup"`
}

type LinksConfig struct {
	Alternate LinkAlternateConfig `json:"alternate" toml:"alternate"`
	Open      OpenConfig          `json:"open" toml:"open"`
}

type LinkAlternateConfig struct {
	Key         string `json:"key" toml:"key"`
	FileCommand string `json:"file_command" toml:"file_command"`
	URLCommand  string `json:"url_command" toml:"url_command"`
}

type OpenConfig struct {
	Mode     string `json:"mode" toml:"mode"`
	PaneSide string `json:"pane_side" toml:"pane_side"`
}

type BookmarksConfig struct {
	Dirs           []string   `json:"dirs" toml:"dirs"`
	IgnorePatterns []string   `json:"ignore_patterns" toml:"ignore_patterns"`
	Open           OpenConfig `json:"open" toml:"open"`
}

type StatusConfig struct {
	StatusDirs     StringList `json:"status_dir" toml:"status_dir"`
	Statuses       StringList `json:"statuses" toml:"statuses"`
	PreviewCommand string     `json:"preview_command" toml:"preview_command"`
	IgnorePatterns []string   `json:"ignore_patterns" toml:"ignore_patterns"`
	Open           OpenConfig `json:"open" toml:"open"`
}

type StringList []string

func (s *StringList) UnmarshalTOML(value interface{}) error {
	switch v := value.(type) {
	case string:
		*s = []string{v}
		return nil
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return fmt.Errorf("string list items must be strings")
			}
			out = append(out, text)
		}
		*s = out
		return nil
	default:
		return fmt.Errorf("value must be a string or string list")
	}
}

type Command struct {
	Title       string `json:"title" toml:"title"`
	Category    string `json:"category,omitempty" toml:"category,omitempty"`
	Description string `json:"description,omitempty" toml:"description,omitempty"`
	Session     string `json:"session,omitempty" toml:"session,omitempty"`
	Mode        string `json:"mode" toml:"mode"`
	Cmd         string `json:"cmd" toml:"cmd"`
	WindowName  string `json:"window_name,omitempty" toml:"window_name,omitempty"`
	WorkingDir  string `json:"working_dir,omitempty" toml:"working_dir,omitempty"`
	Enter       bool   `json:"enter,omitempty" toml:"enter,omitempty"`
}

type QuickDir struct {
	Title   string `json:"title" toml:"title"`
	Path    string `json:"path" toml:"path"`
	Command string `json:"command,omitempty" toml:"command,omitempty"`
	Session string `json:"session,omitempty" toml:"session,omitempty"`
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", configFileName)
	}
	return filepath.Join(home, configFileName)
}

func Default() Config {
	return Config{
		Palette: PaletteConfig{
			Sections: []string{"sessions", "panes"},
		},
		Projects: ProjectsConfig{
			Roots:         []string{"~/projects"},
			BootstrapFile: ".tmux-sessionizer",
		},
		Popup: PopupConfig{
			Width:  "90%",
			Height: "80%",
			Border: "rounded",
		},
		Editor: EditorConfig{
			Command: "$EDITOR",
			Popup: PopupConfig{
				Width:  "80%",
				Height: "80%",
				Border: "rounded",
			},
		},
		Links: LinksConfig{
			Alternate: LinkAlternateConfig{
				Key:         "alt-enter",
				FileCommand: "open -a TextEdit",
				URLCommand:  "open -a Safari",
			},
			Open: OpenConfig{
				Mode:     "popup",
				PaneSide: "right",
			},
		},
		Bookmarks: BookmarksConfig{
			Dirs:           []string{"~/notes/projects/{project}", "~/projects/{project}"},
			IgnorePatterns: []string{".git", ".tmp", "vendor"},
			Open: OpenConfig{
				Mode:     "pane",
				PaneSide: "right",
			},
		},
		Status: StatusConfig{
			StatusDirs:     []string{"./todo"},
			Statuses:       []string{"new", "doing", "done"},
			PreviewCommand: "glow",
			IgnorePatterns: []string{".gitkeep"},
			Open: OpenConfig{
				Mode:     "pane",
				PaneSide: "below",
			},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return LoadForContext("", "")
	}
	if err := loadConfigFile(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}
	normalizeConfig(&cfg)
	applyDefaults(&cfg)
	return cfg, Validate(cfg)
}

func LoadForContext(currentDir string, sessionRoot string) (Config, error) {
	cfg := Default()
	for _, path := range configPaths(currentDir, sessionRoot) {
		if err := loadConfigFile(path, &cfg); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Config{}, err
		}
	}
	normalizeConfig(&cfg)
	applyDefaults(&cfg)
	return cfg, Validate(cfg)
}

func configPaths(currentDir string, sessionRoot string) []string {
	if currentDir == "" {
		currentDir, _ = os.Getwd()
	}
	candidates := []string{DefaultPath()}
	if sessionRoot != "" {
		candidates = append(candidates, filepath.Join(sessionRoot, configFileName))
	}
	if currentDir != "" {
		candidates = append(candidates, filepath.Join(currentDir, configFileName))
	}
	seen := make(map[string]bool)
	paths := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

func loadConfigFile(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if filepath.Ext(path) == ".json" {
		var next Config
		if err := json.Unmarshal(b, &next); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		overlayAll(cfg, next)
		return nil
	}
	var next Config
	meta, err := toml.Decode(string(b), &next)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	overlayDefined(cfg, next, meta)
	return nil
}

func overlayAll(dst *Config, src Config) {
	*dst = src
}

func overlayDefined(dst *Config, src Config, meta toml.MetaData) {
	if meta.IsDefined("palette", "sections") {
		dst.Palette.Sections = src.Palette.Sections
	}
	if meta.IsDefined("picker", "show_help") {
		dst.Picker.ShowHelp = src.Picker.ShowHelp
	}
	if meta.IsDefined("projects", "roots") {
		dst.Projects.Roots = src.Projects.Roots
	}
	if meta.IsDefined("projects", "bootstrap_file") {
		dst.Projects.BootstrapFile = src.Projects.BootstrapFile
	}
	if meta.IsDefined("popup", "width") {
		dst.Popup.Width = src.Popup.Width
	}
	if meta.IsDefined("popup", "height") {
		dst.Popup.Height = src.Popup.Height
	}
	if meta.IsDefined("popup", "border") {
		dst.Popup.Border = src.Popup.Border
	}
	if meta.IsDefined("editor", "command") {
		dst.Editor.Command = src.Editor.Command
	}
	if meta.IsDefined("editor", "popup", "width") {
		dst.Editor.Popup.Width = src.Editor.Popup.Width
	}
	if meta.IsDefined("editor", "popup", "height") {
		dst.Editor.Popup.Height = src.Editor.Popup.Height
	}
	if meta.IsDefined("editor", "popup", "border") {
		dst.Editor.Popup.Border = src.Editor.Popup.Border
	}
	if meta.IsDefined("links", "alternate", "key") {
		dst.Links.Alternate.Key = src.Links.Alternate.Key
	}
	if meta.IsDefined("links", "alternate", "file_command") {
		dst.Links.Alternate.FileCommand = src.Links.Alternate.FileCommand
	}
	if meta.IsDefined("links", "alternate", "url_command") {
		dst.Links.Alternate.URLCommand = src.Links.Alternate.URLCommand
	}
	if meta.IsDefined("links", "open", "mode") {
		dst.Links.Open.Mode = src.Links.Open.Mode
	}
	if meta.IsDefined("links", "open", "pane_side") {
		dst.Links.Open.PaneSide = src.Links.Open.PaneSide
	}
	if meta.IsDefined("bookmarks", "dirs") {
		dst.Bookmarks.Dirs = src.Bookmarks.Dirs
	}
	if meta.IsDefined("bookmarks", "ignore_patterns") {
		dst.Bookmarks.IgnorePatterns = src.Bookmarks.IgnorePatterns
	}
	if meta.IsDefined("bookmarks", "open", "mode") {
		dst.Bookmarks.Open.Mode = src.Bookmarks.Open.Mode
	}
	if meta.IsDefined("bookmarks", "open", "pane_side") {
		dst.Bookmarks.Open.PaneSide = src.Bookmarks.Open.PaneSide
	}
	if meta.IsDefined("status", "status_dir") {
		dst.Status.StatusDirs = src.Status.StatusDirs
	}
	if meta.IsDefined("status", "statuses") {
		dst.Status.Statuses = src.Status.Statuses
	}
	if meta.IsDefined("status", "preview_command") {
		dst.Status.PreviewCommand = src.Status.PreviewCommand
	}
	if meta.IsDefined("status", "ignore_patterns") {
		dst.Status.IgnorePatterns = src.Status.IgnorePatterns
	}
	if meta.IsDefined("status", "open", "mode") {
		dst.Status.Open.Mode = src.Status.Open.Mode
	}
	if meta.IsDefined("status", "open", "pane_side") {
		dst.Status.Open.PaneSide = src.Status.Open.PaneSide
	}
	if meta.IsDefined("commands") {
		dst.Commands = append(dst.Commands, src.Commands...)
	}
	if meta.IsDefined("quick_dirs") {
		dst.QuickDirs = append(dst.QuickDirs, src.QuickDirs...)
	}
}

func normalizeConfig(cfg *Config) {
	cfg.Links.Alternate.Key = strings.TrimSpace(cfg.Links.Alternate.Key)
	for _, open := range []*OpenConfig{
		&cfg.Links.Open,
		&cfg.Bookmarks.Open,
		&cfg.Status.Open,
	} {
		open.Mode = strings.TrimSpace(open.Mode)
		open.PaneSide = strings.TrimSpace(open.PaneSide)
	}
}

func applyDefaults(cfg *Config) {
	def := Default()
	if len(cfg.Palette.Sections) == 0 {
		cfg.Palette.Sections = append([]string(nil), def.Palette.Sections...)
	}
	if cfg.Projects.BootstrapFile == "" {
		cfg.Projects.BootstrapFile = def.Projects.BootstrapFile
	}
	if len(cfg.Projects.Roots) == 0 {
		cfg.Projects.Roots = append([]string(nil), def.Projects.Roots...)
	}
	if cfg.Popup.Width == "" {
		cfg.Popup.Width = def.Popup.Width
	}
	if cfg.Popup.Height == "" {
		cfg.Popup.Height = def.Popup.Height
	}
	if cfg.Popup.Border == "" {
		cfg.Popup.Border = def.Popup.Border
	}
	if cfg.Editor.Command == "" {
		cfg.Editor.Command = def.Editor.Command
	}
	if cfg.Editor.Popup.Width == "" {
		cfg.Editor.Popup.Width = def.Editor.Popup.Width
	}
	if cfg.Editor.Popup.Height == "" {
		cfg.Editor.Popup.Height = def.Editor.Popup.Height
	}
	if cfg.Editor.Popup.Border == "" {
		cfg.Editor.Popup.Border = def.Editor.Popup.Border
	}
	if cfg.Links.Alternate.Key == "" {
		cfg.Links.Alternate.Key = def.Links.Alternate.Key
	}
	if cfg.Links.Alternate.FileCommand == "" {
		cfg.Links.Alternate.FileCommand = def.Links.Alternate.FileCommand
	}
	if cfg.Links.Alternate.URLCommand == "" {
		cfg.Links.Alternate.URLCommand = def.Links.Alternate.URLCommand
	}
	if cfg.Links.Open.Mode == "" {
		cfg.Links.Open.Mode = def.Links.Open.Mode
	}
	if cfg.Links.Open.PaneSide == "" {
		cfg.Links.Open.PaneSide = def.Links.Open.PaneSide
	}
	if len(cfg.Bookmarks.Dirs) == 0 {
		cfg.Bookmarks.Dirs = append([]string(nil), def.Bookmarks.Dirs...)
	}
	if len(cfg.Bookmarks.IgnorePatterns) == 0 {
		cfg.Bookmarks.IgnorePatterns = append([]string(nil), def.Bookmarks.IgnorePatterns...)
	}
	if cfg.Bookmarks.Open.Mode == "" {
		cfg.Bookmarks.Open.Mode = def.Bookmarks.Open.Mode
	}
	if cfg.Bookmarks.Open.PaneSide == "" {
		cfg.Bookmarks.Open.PaneSide = def.Bookmarks.Open.PaneSide
	}
	if len(cfg.Status.StatusDirs) == 0 {
		cfg.Status.StatusDirs = append([]string(nil), def.Status.StatusDirs...)
	}
	if len(cfg.Status.Statuses) == 0 {
		cfg.Status.Statuses = append([]string(nil), def.Status.Statuses...)
	}
	if cfg.Status.PreviewCommand == "" {
		cfg.Status.PreviewCommand = def.Status.PreviewCommand
	}
	if len(cfg.Status.IgnorePatterns) == 0 {
		cfg.Status.IgnorePatterns = append([]string(nil), def.Status.IgnorePatterns...)
	}
	if cfg.Status.Open.Mode == "" {
		cfg.Status.Open.Mode = def.Status.Open.Mode
	}
	if cfg.Status.Open.PaneSide == "" {
		cfg.Status.Open.PaneSide = def.Status.Open.PaneSide
	}
}

func Validate(cfg Config) error {
	seenPaletteSections := make(map[string]bool)
	for i, section := range cfg.Palette.Sections {
		switch section {
		case "agents", "sessions", "panes":
		default:
			return fmt.Errorf("palette.sections[%d] must be one of agents, sessions, panes", i)
		}
		if seenPaletteSections[section] {
			return fmt.Errorf("palette.sections[%d] duplicates %q", i, section)
		}
		seenPaletteSections[section] = true
	}
	for i, dir := range cfg.Bookmarks.Dirs {
		if strings.TrimSpace(dir) == "" {
			return fmt.Errorf("bookmarks.dirs[%d] is required", i)
		}
	}
	if err := validateLinkAlternateKey(cfg.Links.Alternate.Key); err != nil {
		return err
	}
	if err := validateLinkAlternateCommand("links.alternate.file_command", cfg.Links.Alternate.FileCommand); err != nil {
		return err
	}
	if err := validateLinkAlternateCommand("links.alternate.url_command", cfg.Links.Alternate.URLCommand); err != nil {
		return err
	}
	if err := validateOpenConfig("links.open", cfg.Links.Open); err != nil {
		return err
	}
	if err := validateOpenConfig("bookmarks.open", cfg.Bookmarks.Open); err != nil {
		return err
	}
	if err := validateOpenConfig("status.open", cfg.Status.Open); err != nil {
		return err
	}
	for i, c := range cfg.Commands {
		if c.Title == "" {
			return fmt.Errorf("commands[%d].title is required", i)
		}
		if c.Cmd == "" {
			return fmt.Errorf("commands[%d].cmd is required", i)
		}
		switch c.Mode {
		case "popup", "paste", "window", "tmux", "shell":
		default:
			return fmt.Errorf("commands[%d].mode must be one of popup, paste, window, tmux, shell", i)
		}
	}
	for i, d := range cfg.QuickDirs {
		if d.Title == "" {
			return fmt.Errorf("quick_dirs[%d].title is required", i)
		}
		if d.Path == "" {
			return fmt.Errorf("quick_dirs[%d].path is required", i)
		}
	}
	for i, status := range cfg.Status.Statuses {
		if strings.TrimSpace(status) == "" {
			return fmt.Errorf("status.statuses[%d] is required", i)
		}
	}
	return nil
}

func validateLinkAlternateKey(key string) error {
	key = strings.TrimSpace(key)
	if strings.ContainsAny(key, ", \t\r\n") {
		return fmt.Errorf("links.alternate.key must be one fzf key without commas or whitespace")
	}
	switch key {
	case "enter", "alt-1", "alt-2", "alt-3", "alt-4", "alt-5", "alt-6":
		return fmt.Errorf("links.alternate.key %q is reserved for picker behavior", key)
	default:
		return nil
	}
}

func validateLinkAlternateCommand(name string, command string) error {
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(command); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch command[i] {
		case '\\':
			if !inSingle {
				escaped = true
			}
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '{':
			if inSingle && i+1 < len(command) && command[i+1] == '}' {
				return fmt.Errorf("%s must not put {} inside single quotes", name)
			}
		}
	}
	return nil
}

func validateOpenConfig(name string, cfg OpenConfig) error {
	switch strings.TrimSpace(cfg.Mode) {
	case "popup", "pane", "window":
	default:
		return fmt.Errorf("%s.mode must be one of popup, pane, window", name)
	}
	switch strings.TrimSpace(cfg.PaneSide) {
	case "right", "left", "above", "below":
	default:
		return fmt.Errorf("%s.pane_side must be one of right, left, above, below", name)
	}
	return nil
}

func Sample() string {
	return fmt.Sprintf(`[palette]
# Available sections: "agents", "sessions", "panes".
sections = ["sessions", "panes"]

[picker]
# Show helper lines above fzf results.
show_help = false

[projects]
# Root directories scanned by the projects view.
roots = ["~/projects"]
# File that marks a project as having native tmux bootstrap.
bootstrap_file = ".tmux-sessionizer"

[[commands]]
title = "Lazygit"
category = "Tools"
mode = "popup"
cmd = "lazygit"

[[commands]]
title = "Tig"
category = "Tools"
mode = "popup"
cmd = "tig"

[[commands]]
title = "Git status"
category = "Git"
mode = "paste"
cmd = "git status"
# Optional: only show this command in a matching tmux session.
# session = "tmux-menu"
enter = true

[[commands]]
title = "Git diff"
category = "Git"
mode = "paste"
cmd = "git diff"
# session = "tmux-menu"
enter = true

[[quick_dirs]]
title = "documents"
path = "~/Documents"
# Optional: run after changing into the directory.
# command = "git status"

[[quick_dirs]]
title = "config"
path = "~/.config"

[[quick_dirs]]
title = "projects"
path = "~/projects"

[popup]
width = "90%%"
height = "80%%"
border = "rounded"

[editor]
command = "$EDITOR"

[editor.popup]
width = "80%%"
height = "80%%"
border = "rounded"

[links.alternate]
# Secondary opener used by Alt-Enter in the links view.
# Use {} when the target must appear before later command arguments.
key = "alt-enter"
file_command = "open -a TextEdit"
url_command = "open -a Safari"

[links.open]
# Enter opener for file links: popup, pane, or window.
mode = "popup"
# Used when mode = "pane": right, left, above, or below.
pane_side = "right"

[bookmarks]
# Markdown directories scanned by the bookmarks view, in display order.
# {project} expands to the current tmux project/session name.
dirs = ["~/notes/projects/{project}", "~/projects/{project}"]
ignore_patterns = [".git", ".tmp", "vendor"]

[bookmarks.open]
# Opener for local file bookmarks: popup, pane, or window.
mode = "pane"
pane_side = "right"

[status]
# Relative paths resolve from the current tmux session root.
status_dir = ["./todo"]
# Status subdirectories shown under each status_dir, in display order.
statuses = ["new", "doing", "done"]
preview_command = "glow"
ignore_patterns = [".gitkeep"]

[status.open]
# Opener for selected status files: popup, pane, or window.
mode = "pane"
pane_side = "below"
`)
}
