# tmux-menu Usage

## What It Does

`tmux-menu` is a small tmux utility for people who keep multiple projects,
shells, and coding agents open at once. It uses `tmux display-popup` and
`fzf --ansi`; there is no custom TUI framework.

Views:

- `palette`: switch to configured tmux sessions, panes, and optionally agents.
- `agents`: switch to panes running agent-like commands with status badges.
- `tools`: run configured commands, Makefile targets, and quick directory jumps.
- `projects`: create or switch tmux sessions for project directories.
- `links`: open URLs and file references captured from pane scrollback.
- `bookmarks`: open inline Markdown links from configured directories.
- `status`: browse task files grouped by status directories.

## Commands

Popup commands:

```sh
tmux-menu popup palette
tmux-menu popup agents
tmux-menu popup tools
tmux-menu popup projects
tmux-menu popup links
tmux-menu popup bookmarks
tmux-menu popup status
```

Non-popup commands are also available:

```sh
tmux-menu palette
tmux-menu agents
tmux-menu tools
tmux-menu projects
tmux-menu links
tmux-menu bookmarks
tmux-menu status
tmux-menu sample-config
```

Inside picker views:

- `Alt-1`: palette
- `Alt-2`: agents
- `Alt-3`: tools
- `Alt-4`: projects
- `Alt-5`: status
- `Alt-6`: bookmarks
- `Alt-Enter` in links: alternate opener

The helper line for these shortcuts is hidden by default. Enable it with
`picker.show_help = true`.

## tmux Bindings

```tmux
bind -n C-Space run-shell "tmux-menu popup palette"
bind -n M-a run-shell "tmux-menu popup agents"
bind -n T run-shell "tmux-menu popup tools"
bind -n M-p run-shell "tmux-menu popup projects"
bind -n M-l run-shell "tmux-menu popup links"
bind -n M-b run-shell "tmux-menu popup bookmarks"
bind -n M-Space run-shell "tmux-menu popup status"
```

## Configuration Loading

Global config:

```text
~/.tmux-menu.conf
```

Local overlays:

- `.tmux-menu.conf` in the tmux session root
- `.tmux-menu.conf` in the origin pane directory

Local scalar/list settings replace earlier values. `[[commands]]` and
`[[quick_dirs]]` entries append to earlier config layers.

Generate a starting config:

```sh
tmux-menu sample-config > ~/.tmux-menu.conf
```

The repository copy is [examples/config.toml](../examples/config.toml).

## Complete Example

```toml
[palette]
sections = ["sessions", "panes"]

[picker]
show_help = false

[projects]
roots = ["~/projects"]
bootstrap_file = ".tmux-sessionizer"

[[commands]]
title = "Lazygit"
category = "Tools"
mode = "popup"
cmd = "lazygit"

[[commands]]
title = "Git status"
category = "Git"
mode = "paste"
cmd = "git status"
enter = true

[[quick_dirs]]
title = "docs"
path = "~/Documents"

[[quick_dirs]]
title = "project config"
path = "./config"
session = "my_project"
command = "ls"

[popup]
width = "90%"
height = "80%"
border = "rounded"

[editor]
command = "$EDITOR"

[editor.popup]
width = "80%"
height = "80%"
border = "rounded"

[links.alternate]
key = "alt-enter"
file_command = "open -a TextEdit"
url_command = "open -a Safari"

[links.open]
mode = "popup"
pane_side = "right"

[bookmarks]
dirs = ["~/notes/projects/{project}", "~/projects/{project}"]
ignore_patterns = [".git", ".tmp", "vendor"]

[bookmarks.open]
mode = "pane"
pane_side = "right"

[status]
status_dir = ["./todo"]
statuses = ["new", "doing", "done"]
preview_command = "glow"
ignore_patterns = [".gitkeep"]

[status.open]
mode = "pane"
pane_side = "below"
```

## Configuration Reference

### `palette`

- `sections`: ordered list of `agents`, `sessions`, and `panes`.

Default:

```toml
[palette]
sections = ["sessions", "panes"]
```

### `picker`

- `show_help`: show the Alt-key helper line above fzf results.

### `projects`

- `roots`: one-level project directories to scan.
- `bootstrap_file`: file to run once when a project session is first created.

Dots in project directory names become underscores in tmux session names. If
multiple projects would get the same session name, tmux-menu adds a stable hash
suffix.

Bootstrap scripts must exit. Queue interactive work into tmux with
`tmux send-keys` instead of blocking inside the bootstrap script.

Example bootstrap:

```sh
#!/bin/sh
tmux send-keys "make dev" C-m
```

### `commands`

Each `[[commands]]` entry supports:

- `title`: display name.
- `category`: optional grouping label.
- `description`: optional extra label text.
- `session`: optional tmux session filter.
- `mode`: one of `popup`, `paste`, `window`, `tmux`, `shell`.
- `cmd`: command to run or paste.
- `window_name`: optional name for `window` mode.
- `working_dir`: optional start directory for `popup` and `window` mode.
- `enter`: for `paste`, press Enter after pasting.

Examples:

```toml
[[commands]]
title = "Git diff"
category = "Git"
mode = "paste"
cmd = "git diff"
enter = true

[[commands]]
title = "Logs"
category = "Dev"
mode = "window"
cmd = "make logs"
window_name = "logs"
```

### `quick_dirs`

Each `[[quick_dirs]]` entry supports:

- `title`: display name.
- `path`: target directory. Relative paths resolve from the tmux session root.
- `command`: optional command to run after `cd`.
- `session`: optional tmux session filter.

Example:

```toml
[[quick_dirs]]
title = "docs"
path = "./docs"
command = "ls"
session = "my_project"
```

### `popup`

Controls the main tmux popup:

- `width`
- `height`
- `border`

### `editor`

- `command`: editor command for local files. Defaults to `$EDITOR`, then
  falls back to `nvim` if empty.

`[editor.popup]` controls editor popup size when an opener uses
`mode = "popup"`.

### `links`

`links` captures origin pane scrollback and extracts:

- `http://` and `https://` URLs
- absolute file paths with optional line/column/range suffixes
- relative file paths resolved from the origin pane directory

Enter first copies the target to the macOS clipboard. URL rows run `open`.
File rows use `[links.open]`.

`[links.open]`:

- `mode`: `popup`, `pane`, or `window`
- `pane_side`: `right`, `left`, `above`, or `below`

`[links.alternate]`:

- `key`: fzf expect key, default `alt-enter`
- `file_command`: alternate file opener
- `url_command`: alternate URL opener

`Alt-1` through `Alt-6` are reserved for view switching and cannot be used as
the alternate opener key. `enter`, commas, and whitespace are also invalid.

Use `{}` when the target must appear before later command arguments. tmux-menu
passes the target as shell data. If `{}` is omitted, the quoted target is
appended. Do not put `{}` inside single quotes; unquoted and double-quoted
placeholders are supported.

### `bookmarks`

`bookmarks` scans inline Markdown links from configured directories.

- `dirs`: directories to scan, in display order.
- `ignore_patterns`: path parts or basenames to skip.

`{project}` expands to the current tmux project/session name.

Only inline links shaped like `[label](target)` are shown. Raw URLs and image
links are ignored. HTTP(S) targets open with `open`; local file targets resolve
relative to the Markdown source file and use `[bookmarks.open]`.

### `status`

`status` shows task files under configured roots.

- `status_dir`: one or more roots, usually `["./todo"]`.
- `statuses`: visible subdirectories and display order.
- `preview_command`: command for fzf preview. If it contains `{}`, the file
  path replaces that placeholder; otherwise the file path is appended.
- `ignore_patterns`: basenames or relative paths to hide.

Rows are:

```text
<status> | <title> | <summary>
```

The title comes from the filename. The summary is the first `summary: ...` line
or `(no summary)`.

`[status.open]` controls how selected files open.

## Use Cases

### Jump Between Projects

Use `projects` when you want one tmux session per project directory. Add
multiple roots if your code lives in more than one parent directory:

```toml
[projects]
roots = ["~/projects", "~/work"]
```

### Keep Coding Agents Visible

Use `agents` when you run agents in separate panes. tmux-menu detects known
agent commands and uses pane/window titles for richer status when available.

### Open File References From Build Output

Use `links` after a compiler, test runner, or search command prints paths like:

```text
internal/config/config.go:42
```

Selecting the row opens the file at the referenced line.

### Browse Project Notes

Use `bookmarks` for Markdown notes that contain links to docs, runbooks, local
files, or dashboards.

```toml
[bookmarks]
dirs = ["~/notes/projects/{project}", "~/projects/{project}/docs"]
```

### Track Lightweight Tasks

Use `status` with a directory like:

```text
todo/
  new/
  doing/
  done/
```

Each file can include:

```text
summary: Short task summary
```

## Validation

Targeted checks:

```sh
GOCACHE=/tmp/tmux-menu-go-build make test
GOCACHE=/tmp/tmux-menu-go-build make build
GOCACHE=/tmp/tmux-menu-go-build make sample-config
```

Broader local check:

```sh
GOCACHE=/tmp/tmux-menu-go-build make validate
```
