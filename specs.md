# tmux-menu Spec

## Goal

Build a small Go tool for tmux users who keep several project sessions open.
The tool has seven user-facing views:

1. `palette`: fuzzy-select tmux sessions and panes.
2. `agents`: fuzzy-select panes running known agent processes.
3. `tools`: fuzzy-select configured commands and quick dirs.
4. `links`: fuzzy-select file and URL references from the active pane scrollback.
5. `bookmarks`: fuzzy-select Markdown links from project notes and project files.
6. `status`: fuzzy-select task files under a configured status directory.
7. `projects`: fuzzy-select a project and create/switch tmux sessions natively.

The first implementation favors low complexity over custom UI polish. It uses:

- Go for tmux discovery, config parsing, dispatch, and safety.
- `fzf` for fuzzy selection inside a tmux popup.
- `tmux display-popup` as the launcher surface.

## Commands

```sh
tmux-menu popup palette
tmux-menu popup agents
tmux-menu popup tools
tmux-menu popup projects
tmux-menu popup links
tmux-menu popup bookmarks
tmux-menu popup status
tmux-menu palette
tmux-menu agents
tmux-menu tools
tmux-menu projects
tmux-menu links
tmux-menu bookmarks
tmux-menu status
tmux-menu sample-config
```

Inside `fzf` picker views:

- `Tab`: next configured view.
- `Shift-Tab`: previous configured view.
- `Alt-1`: main palette
- `Alt-2`: agents
- `Alt-3`: tools
- `Alt-4`: projects
- `Alt-5`: status
- `Alt-6`: bookmarks

In the links view, `Alt-Enter` opens the selected row with the configured
secondary opener.

The shortcuts are shown in a persistent footer at the bottom of every picker.
`picker.tab_order` controls the Tab cycle and defaults to palette, agents,
tools, projects, status, then bookmarks. Configured values may also include
links. Cycling wraps at both ends; a view omitted from the order enters at the
first item with Tab or the last item with Shift-Tab.
Empty views contain a non-selectable `No items` row so navigation keys continue
to work; Enter on that row cancels the picker.

Recommended tmux bindings:

```tmux
bind -n C-Space run-shell "tmux-menu popup palette"
bind -n M-a run-shell "tmux-menu popup agents"
bind -n T run-shell "tmux-menu popup tools"
bind -n M-p run-shell "tmux-menu popup projects"
bind -n M-l run-shell "tmux-menu popup links"
bind -n M-b run-shell "tmux-menu popup bookmarks"
bind -n M-Space run-shell "tmux-menu popup status"
```

Development commands:

```sh
make build
make test
make race
make vet
make validate
make coverage
make install
make config
make run
make palette
make agents
make tools
make projects
make links
make bookmarks
make status
```

Runtime config lives at:

```text
~/.tmux-menu.conf
```

The repo carries a copy of the current working config at:

```text
examples/config.toml
```

Required runtime tools are `tmux` and `fzf`; missing binaries or non-cancel
runtime failures are surfaced as errors. `glow` is only the default status
preview command and can be replaced with `status.preview_command`. File opening
uses `editor.command`, then `$EDITOR`, then `nvim`.

## Palette Mode

`palette` combines configured sections from `palette.sections`.
Default order:

- all tmux sessions
- all tmux panes from all sessions

Allowed sections are `agents`, `sessions`, and `panes`; each section may appear
at most once. For example, `sections = ["agents", "sessions", "panes"]` puts
agent rows back at the top of the main palette, while `["sessions", "panes"]`
keeps the default split where agents live only in the dedicated `agents` view.

Session and pane entries use stable tmux IDs (`#{pane_id}`, `#{window_id}`,
`#{session_id}`) instead of names/indexes for dispatch. Selecting a session
switches client to that session. Selecting a pane switches client, window, and
pane. Window rows are intentionally not shown.

Local shell titles like `user@workstation:~/project` are cleaned before
display so the local machine name does not dominate the picker. Session rows do
not show a working directory.

Normal pane rows include `<window_name> | <pane_title>` when the window's
`automatic-rename` option is disabled. Split panes repeat the manual window name
and retain their distinct pane titles. Automatically renamed windows preserve
the pane-title-only display, and identical window and cleaned pane titles are
not duplicated. Agent rows and status detection continue to use pane/window
titles independently of this normal-pane display rule.

The picker uses ANSI colors through `fzf --ansi`:

- row kinds: blue
- palette session and project names: cyan + bold
- agent-tree session headers: bold, using that session root's configured
  `[session].color` (cyan by default); named options cover terminal default,
  the normal and bright ANSI palettes, and orange
- ordinary pane commands: green
- remote commands such as `mosh` and `ssh`: yellow
- agent product, status, tree, thread, and workdir colors: configured through
  `[agents.colors]`; defaults are blue Codex, orange Claude, magenta other,
  green working, yellow waiting, red attention, and dim unknown/tree/workdir
- metadata: dim

No Bubble Tea dependency is used for this. Use Bubble Tea only if the app needs
a custom multi-pane layout, persistent hotkeys, or richer inline actions that
fzf cannot express cleanly.

## Agents Mode

`agents` lists panes matched by command/title or descendant process (`codex`,
`claude`, `opencode`, `aider`, `cursor-agent`, `gemini`). Selecting a row
switches client, window, and pane.

The dedicated picker groups agents as a tree under tmux session headers.
Session headers are display-only, omit window and pane indexes, and use the
`[session].color` loaded from that tmux session's root `.tmux-menu.conf`.
Selectable child rows show the tree branch, status sign, product mark, thread
name, and workdir. By default Codex uses a blue `>`, Claude uses an orange `✳`,
and other detected agents retain their product name. `[agents.icons]` configures
all product, current-thread, tree, and status symbols; `[agents.colors]`
configures all non-session colors. The foreground tmux command
is omitted because launchers can expose generic values such as `node` or a
version number instead of the product name. Displayed workdirs omit a leading
`~/projects/`. The first agent row is selected when the picker opens; selecting
a session header does not dispatch an action.

The current agent thread is marked by prefixing its thread name with `*` without
a space. Canonical UUID-shaped agent titles and tmux session names are shortened
to their first segment. The picker shows recent
scrollback from the selected pane in a visible right-side preview and follows
its latest lines. `picker.preview_width` controls the preview split for this
and every other preview-enabled picker. Agent entries embedded in the optional
main-palette `agents` section keep the compact flat row layout.

Agent rows start with a status sign. Codex status comes from `#{pane_title}` or
`#{window_name}` because process state is not a reliable wait/work signal for
Codex. Codex exposes terminal title items through `tui.terminal_title` and
`/title`; accepted title shapes include `Ready`, `Working`, `Thinking`,
`Action Required`, `<spinner> <state>`, `<current-dir>|<state>`,
`<current-dir>|<thread-title>|<state>`, and spinner-only
`<marker>|<thread-title>` forms.
When the title is only a run state, the row title falls back to the pane
directory name.

- bold red `!` (`attention`): final state is `Action Required`
- green `●` (`working`): final state is `Working` or `Thinking`
- yellow `○` (`waiting`): final state is `Ready`
- green `●` (`working`): spinner-only title has a leading braille/dot marker
- yellow `○` (`waiting`): spinner-only title has an empty marker before `|`

Claude Code status comes from the leading `#{pane_title}` marker when present:
star-like idle markers show yellow `○`, animated spinner markers show green
`●`, and explicit needs-input/permission text shows bold red `!`.

Other agents use process state:

- green `●` (`working`): an agent process in the pane tree is running
- yellow `○` (`waiting`): the agent process is present but sleeping
- dim `?` (`unknown`): process state is unavailable

## Tools Mode

`tools` combines configured sources plus local Makefile targets:

- user commands from `~/.tmux-menu.conf` plus local `.tmux-menu.conf` overlays
- simple targets from `Makefile` in the session root and origin pane directory
- quick directories from `~/.tmux-menu.conf` plus local `.tmux-menu.conf` overlays

Configured command modes:

- `popup`: run command in a new tmux popup after the picker closes, starting in
  the origin pane directory unless `working_dir` is configured
- `paste`: paste command into the origin pane; optional `enter`
- `window`: open a new tmux window and run command there, starting in the origin
  pane directory unless `working_dir` is configured
- `tmux`: run raw tmux command
- `shell`: run shell command after the picker closes

Commands may set `session` to only show in a matching tmux session. Commands
appear first, then Makefile targets, then quick dirs. Quick dirs paste
`cd <path>` into the origin pane and press Enter. If a quick dir has `command`,
it pastes `cd <path> && <command>` instead.
Paste mode writes through a temporary named tmux buffer and deletes that buffer
after paste, so the user's global tmux paste buffer is preserved.

If the current tmux session path or origin pane path contains a `Makefile`, the
tools view adds simple make targets as `make` rows and ignores variable
assignments. Selecting one pastes and executes `make -C <dir> <target>` in the
origin pane; home-relative directories are emitted as `$HOME/...`.

Absolute quick-dir paths and `~/...` paths are used as-is. Relative paths such
as `./docs` resolve against the tmux session path (`#{session_path}`), with the
origin pane path as a fallback. A quick dir may set `session` to only show in a
matching tmux session and `command` to run after changing directory.

## Projects Mode

`projects` lists one-level directories under `projects.roots`. Missing roots
are skipped. Selecting a project switches to an existing tmux session named from
the project directory, or creates a detached session in that project root. Dots
in directory names are replaced with underscores for tmux session names.
If multiple configured projects derive the same session name, the colliding
rows append a stable short hash derived from the full project path, and the
explicit session name is shown in the project row.

When the session is newly created, `tmux-menu` runs `projects.bootstrap_file`
once in the first pane if the file exists.
Project rows show `bootstrap` when `projects.bootstrap_file` exists in the
project root, otherwise `no bootstrap`. The default bootstrap filename is
`.tmux-sessionizer`.
The bootstrap command must eventually exit so `tmux-menu` can receive its
`tmux wait-for` signal. Waiting is bounded to 30 seconds; timeout is reported as
an error instead of hanging indefinitely.

## Links Mode

`links` captures the active/origin pane scrollback and extracts:

- URLs whose scheme appears in `links.url_schemes`, defaulting to `http`,
  `https`, `slack`, and `tg`; configured scheme names are case-insensitive and
  local config replaces the inherited list
- absolute file paths with optional `:line`, `:line:column`, or `:start-end`
- relative file paths with optional `:line`, `:line:column`, or `:start-end`,
  resolved from the origin pane directory

Selecting a row with Enter first copies the URL or resolved file path to the
macOS clipboard. File rows then open `$EDITOR` in a tmux popup at the
referenced line. The editor command and popup size/border come from the
`editor` config, defaulting to `$EDITOR` and `80% x 80%` with a rounded border.
URL rows then run `open <url>` so macOS opens it in the default browser.
`links.open` controls file-row Enter behavior with `mode = "popup"`, `"pane"`,
or `"window"`. When `mode = "pane"`, `pane_side` may be `"right"`, `"left"`,
`"above"`, or `"below"`.

Selecting a row with `Alt-Enter` also copies the target, then uses
`links.alternate`. By default file rows run `open -a TextEdit <file>` and URL
rows run `open -a Safari <url>`. The command may include `{}` when the target
must appear before later arguments; tmux-menu passes the target as shell data.
Otherwise the quoted target is appended. Single-quoted placeholders are
rejected because shell positional arguments do not expand inside single quotes.

The popup launcher passes `TMUX_MENU_ORIGIN_PANE` and `TMUX_MENU_ORIGIN_PATH`
so the links parser reads the pane that invoked the popup, not the temporary
popup pane.

All picker views require a readable tmux runtime context. Failures from
`tmux display-message` are reported immediately instead of falling back to empty
pane, path, or session values.

## Bookmarks Mode

`bookmarks` scans inline Markdown links from `.md` files under
`bookmarks.dirs`. The project name comes from the tmux session path when
available, then the origin pane path, then the session name.

The default source order is:

1. `~/notes/projects/{project}`
2. `~/projects/{project}`

`{project}` expands to the current tmux project/session name. Configured
directories are scanned recursively in order. Missing roots are skipped. Path
parts matching `bookmarks.ignore_patterns` are skipped; the default is
`[".git", ".tmp", "vendor"]`. Only inline links shaped like `[label](target)`
are shown; raw URLs and image links are ignored.

Rows start with the source directory name and then show the Markdown label,
target, and source file with a line number. Home paths are displayed with `~`.

Selecting an `http://` or `https://` target runs `open <url>` so macOS opens it
in the default browser. Selecting a local file target resolves it relative to
the Markdown file containing the link, strips fragment/query suffixes, and opens
the resolved file with `editor.command` using `bookmarks.open`. The default is a
tmux pane split to the right of the origin pane.

## Status Mode

`status` shows a lane-ordered task board from each `status.status_dir`. Relative
paths resolve against the tmux session root (`#{session_path}`), with the origin
pane path as a fallback when the session root is unavailable. The default root is
`["./todo"]`.

Only configured `status.statuses` subdirectories are shown, in configured order.
The default is `["new", "doing", "done"]`. Extra directories such as `old` are
hidden unless configured.

Rows show:

```text
<status> | <title> | <summary>
```

The title comes from the filename without extension, with `-` and `_` replaced
by spaces. The summary comes from the first `summary: ...` line and falls back
to `(no summary)`. Files are sorted by path within each configured
status lane. Files whose basename or relative path matches
`status.ignore_patterns` are hidden; the default ignores `.gitkeep`.

The picker uses `fzf --preview`, hidden by default with `Space` bound to toggle
the full preview. The preview command comes from `status.preview_command`,
defaulting to `glow`. If the configured command contains `{}`, that placeholder
is replaced with the hidden selected file path; otherwise the selected file path
is appended. The right-side split uses the shared `picker.preview_width`.

Selecting a status row opens the selected file with the configured editor using
`status.open`, started from the tmux session root. The default is a tmux pane
below the origin pane.

## Config

Path:

```text
~/.tmux-menu.conf
```

If `.tmux-menu.conf` exists in the tmux session root or origin pane directory,
it is loaded after the global config. Local scalar/list values replace earlier
values; `[[commands]]` and `[[quick_dirs]]` entries are appended.

Example:

```toml
[palette]
sections = ["sessions", "panes"]

[picker]
show_help = false
preview_width = "60%"
tab_order = ["palette", "agents", "tools", "projects", "status", "bookmarks"]

[session]
color = "cyan"

[agents.icons]
codex = ">"
claude = "✳"
current = "*"
working = "●"
waiting = "○"

[agents.colors]
codex = "blue"
claude = "orange"
working = "green"
waiting = "yellow"

[projects]
roots = ["~/projects"]
bootstrap_file = ".tmux-sessionizer"

[[commands]]
title = "Git status"
category = "Git"
mode = "paste"
cmd = "git status"
# session = "tmux-menu"
enter = true

[[quick_dirs]]
title = "documents"
path = "~/Documents"
# command = "git status"

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

[links]
url_schemes = ["http", "https", "slack", "tg"]

[links.open]
mode = "popup"
pane_side = "right"

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

## Validation

Minimum checks:

```sh
go test ./...
go build ./cmd/tmux-menu
./tmux-menu sample-config
```

Preferred local commands:

```sh
GOCACHE=/tmp/tmux-menu-go-build make test
GOCACHE=/tmp/tmux-menu-go-build make build
GOCACHE=/tmp/tmux-menu-go-build make sample-config
GOCACHE=/tmp/tmux-menu-go-build make validate
GOCACHE=/tmp/tmux-menu-go-build make coverage
```

Manual checks inside tmux:

```sh
./tmux-menu popup palette
./tmux-menu popup projects
./tmux-menu popup links
./tmux-menu popup status
```

## Things To Implement Next

- Dynamic tool commands with placeholders:
  - `{origin}`: origin pane working directory
  - `{session_root}`: current tmux session path
  - `{query}`: prompt for text before running the command

Example:

```toml
[[commands]]
title = "rg query"
category = "Search"
mode = "popup"
cmd = "rg -n {query} {origin}"
```
