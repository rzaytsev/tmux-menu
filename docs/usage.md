# tmux-menu Usage

## What It Does

`tmux-menu` is a small tmux utility for people who keep multiple projects,
shells, and coding agents open at once. Picker views use `tmux display-popup`
and `fzf --ansi`. The continuously refreshing Agents monitor is the one custom
Bubble Tea view because a persistent multi-pane layout is outside fzf's model.

Views:

- `palette`: switch to configured tmux sessions, panes, and optionally agents.
- `agents`: monitor live agent output, status, attention, and timing; delegate
  fuzzy switching to the existing fzf picker.
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
tmux-menu popup agents --picker
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
tmux-menu agents --picker
tmux-menu tools
tmux-menu projects
tmux-menu links
tmux-menu bookmarks
tmux-menu status
tmux-menu validate-config ~/.tmux-menu.conf
tmux-menu sample-config
```

Optional identity-bound provider metadata:

```sh
tmux-menu agent-hook snippets [codex|claude] [ingest|trace]
tmux-menu agent-hook doctor
tmux-menu agent-hook snapshot
```

`snippets` prints reviewable provider configuration without editing it.
`doctor` checks the active hook wiring read-only. `snapshot` emits current live
agent metadata only; it never includes terminal output, prompts, model prose,
tool payloads, cwd, or a transcript. `trace` is an opt-in metadata-only
diagnostic mode.

Inside the Agent HUD:

- arrows or `h`, `j`, `k`, `l`: move selection
- `[` / `]`: previous/next page
- `n`: select the next attention item across all pages
- `z`: toggle focused terminal view
- `?`: toggle full help
- `/`: suspend the HUD and open the fzf Agents picker
- `Enter`: switch to the exact selected live pane
- `q`: quit

Canceling the picker opened with `/` resumes the same HUD selection, page, and
focus when that pane is still live. Canceling `agents --picker` exits directly.
The picker retains `Ctrl-R` refresh and `Ctrl-X` exact completion
acknowledgement; HUD-native keys never acknowledge, approve, or type into an
agent.

Inside fzf picker views:

- `Tab`: next view in `picker.tab_order`
- `Shift-Tab`: previous view in `picker.tab_order`
- `Alt-1`: palette
- `Alt-2`: agents
- `Alt-3`: tools
- `Alt-4`: projects
- `Alt-5`: status
- `Alt-6`: bookmarks
- `Alt-Enter` in links: alternate opener

The navigation shortcuts are shown in a footer at the bottom of every picker.
Empty views show `No items` and remain navigable with the same shortcuts.

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

Validate one config or a layered global/project pair. Validation rejects
misspelled or otherwise unknown keys instead of silently ignoring them:

```sh
tmux-menu validate-config ~/.tmux-menu.conf
tmux-menu validate-config ~/.tmux-menu.conf /path/to/project/.tmux-menu.conf
```

The repository copy is [examples/config.toml](../examples/config.toml).

## Complete Example

```toml
[palette]
sections = ["sessions", "panes"]

[picker]
show_help = false
preview_width = "60%"
tab_order = ["palette", "agents", "tools", "projects", "status", "bookmarks"]

[session]
color = "cyan"

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

[links]
url_schemes = ["http", "https", "slack", "tg"]

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
report_timeout = "3s"
status_dir = ["./todo"]
statuses = ["new", "doing", "done"]
preview_command = "glow"
ignore_patterns = [".gitkeep"]

# Uncomment to replace the single-project modes with a cross-project radar.
# [[status.targets]]
# title = "Example"
# session = "example"
# command = "./scripts/status-report"

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

- `show_help`: show optional view-specific helper text.
- `preview_width`: width of the right-side preview in every preview-enabled
  picker. Defaults to `60%`; its height fills the picker popup.
- `tab_order`: picker views visited by `Tab` and `Shift-Tab`, in order. Valid
  values are `palette`, `agents`, `tools`, `projects`, `links`, `status`, and
  `bookmarks`. The default omits `links`, matching the numbered shortcuts.

### `session`

- `color`: bold session-label color in the Agent HUD and direct picker. Valid values are
  `default`, `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`,
  `bright_black`, `bright_red`, `bright_green`, `bright_yellow`, `bright_blue`,
  `bright_magenta`, `bright_cyan`, `bright_white`, and `orange`. The built-in
  default is `cyan`; `default` uses the terminal's foreground color.

Put this setting in the `.tmux-menu.conf` at a tmux session root to give that
session its own header color:

```toml
[session]
color = "red"
```

### `agents`

`agents.popup_width` controls the dedicated HUD and Agents-picker popup width;
the default is `100%`. V1 has no refresh, capture, or layout configuration.

`[agents.icons]` configures every symbolic part of the HUD and agent rows:
`codex`, `claude`, `other`, `current`, `branch`, `last_branch`, `attention`,
`working`, `completed`, `waiting`, and `unknown`. An empty `other` keeps the
detected product name.

`[agents.colors]` configures `codex`, `claude`, `other`, `branch`, `thread`,
`workdir`, `attention`, `working`, `completed`, `waiting`, and `unknown`. Colors
accept every `session.color` value plus `dim`.

```toml
[agents]
popup_width = "100%"

[agents.icons]
codex = ">"
claude = "✳"
other = ""
current = "*"
branch = "├─"
last_branch = "└─"
attention = "!"
working = "●"
completed = "✓"
waiting = "○"
unknown = "?"

[agents.colors]
codex = "blue"
claude = "orange"
other = "magenta"
branch = "dim"
thread = "default"
workdir = "dim"
attention = "red"
working = "green"
completed = "bright_cyan"
waiting = "yellow"
unknown = "dim"
```

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

Controls all picker tmux popups:

- `width`: popup width, default `90%`.
- `height`: popup height, default `80%`; this also controls preview height.
- `border`

### `editor`

- `command`: editor command for local files. Defaults to `$EDITOR`, then
  falls back to `nvim` if empty.

`[editor.popup]` controls editor popup size when an opener uses
`mode = "popup"`.

### `links`

`links` captures origin pane scrollback and extracts:

- URLs whose scheme is listed in `links.url_schemes`; defaults are `http`,
  `https`, `slack`, and `tg`
- absolute file paths with optional line/column/range suffixes
- relative file paths resolved from the origin pane directory

```toml
[links]
url_schemes = ["http", "https", "slack", "tg"]
```

Local config replaces the inherited list. Scheme names are case-insensitive and
must use URI scheme syntax. Add schemes such as `mailto`, `zoommtg`, or
`obsidian` only when useful; keeping the list narrow avoids unrelated text in
scrollback becoming selectable links.

Enter first copies the target to the macOS clipboard. URL rows run `open`.
File rows use `[links.open]`.

`[links.open]`:

- `mode`: `popup`, `pane`, or `window`
- `pane_side`: `right`, `left`, `above`, or `below`

`[links.alternate]`:

- `key`: fzf expect key, default `alt-enter`
- `file_command`: alternate file opener
- `url_command`: alternate URL opener

`Tab`, `Shift-Tab`, and `Alt-1` through `Alt-6` are reserved for view switching
and cannot be used as the alternate opener key. `enter`, commas, and whitespace
are also invalid.

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

`status` has three modes, selected in this order:

1. When `[[status.targets]]` entries exist, it shows a cross-project radar.
2. Otherwise, `command` runs a project-owned status interface.
3. Otherwise, it shows task files under configured roots.

Radar targets are active tmux sessions. Each target has:

- `title`: optional display title; defaults to `session`.
- `session`: exact tmux session name.
- `command`: reporter command, run from that session's root.

`report_timeout` is applied independently to every reporter and defaults to
`3s`. Reporters run concurrently, so one slow project does not delay all others.
Missing sessions, command failures, timeouts, and invalid output appear as
`unknown` rows instead of closing the picker. Rows are sorted by `attention`,
`warning`, `unknown`, then `ok`; Enter switches using the target's stable tmux
session ID. The right preview is visible by default and preserves reporter block
order.

The reporter must print one JSON object:

```json
{
  "blocks": [
    {
      "title": "Agents",
      "status": "attention",
      "summary": "Permission required",
      "details": "Codex in pane %3 is waiting for approval"
    },
    {
      "title": "Git",
      "status": "warning",
      "summary": "3 changed files"
    }
  ]
}
```

Every block requires `title`, `status`, and `summary`; `details` is optional.
Valid statuses are `attention`, `warning`, `ok`, and `unknown`. tmux-menu derives
the row status and its top two problem summaries from the blocks. The command
output is limited to 1 MiB. The reporter process receives
`TMUX_MENU_SESSION_ID`, `TMUX_MENU_SESSION_NAME`, and
`TMUX_MENU_SESSION_PATH` for the target plus the caller's
`TMUX_MENU_ORIGIN_PANE` and `TMUX_MENU_ORIGIN_PATH`.

A portable Git/task example is
[`examples/status-report.sh`](../examples/status-report.sh). For a quick trial:

```toml
[status]
report_timeout = "3s"

[[status.targets]]
title = "tmux-menu"
session = "tmux-menu"
command = "sh /path/to/tmux-menu/examples/status-report.sh"
```

When the radar is not configured, the directory board uses:

- `command`: optional shell command that replaces the directory-based picker;
  it runs from the tmux session root with the `TMUX_MENU_*` context variables.
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

Run `tmux-menu popup agents` when each agent has its own tmux pane. The HUD
discovers live panes continuously, shows at most four bounded terminal tails per
page, and keeps surviving pane IDs in stable cells when status changes. Narrow
popups reduce the grid to one selected terminal. `z` focuses that terminal,
`n` jumps to attention anywhere in the inventory, and the summary keeps
cross-page status counts visible.

Status is normalized as `attention`, `working`, `completed`, `waiting`, or
`unknown`. Codex and Claude hook evidence takes precedence only while it still
matches the live tmux server, pane ID/PID, session, provider, provider session,
and provider-process incarnation. Titles and process state provide restricted
fallbacks. Stored hook evidence never creates a row without a live pane.

The optional clocks have distinct meanings:

- `turn`: elapsed time since the current trusted provider turn began
- `state`: time spent in the current normalized state
- `event`: age since the last trusted provider event

Missing hook timestamps are omitted; tmux-menu does not estimate tokens, cost,
context use, task percent, tool counts, or file ownership. Child counts are
rolled up only when fresh authoritative metadata exists.

Each refresh is observational and serial. A disposable-socket baseline completed
inventory and styled capture below the timer's 0.01s display resolution; v1 uses
a conservative 900 ms generation deadline and starts the next generation about
one second after completion. It captures only the current
page (maximum four panes) or the focused pane, up to 200 lines and 256 KiB per
pane, under a 13 MiB shared output budget and a 256 KiB retained terminal
budget. The generation charges pane/process output plus bounded hook/config
reads to that cap, and rechecks pane ID/PID after capture before accepting a
tail. Hidden pages retain no terminal tail. A failed pane capture keeps the
last safe tail with a visible stale marker; vanished or reused identities drop
their retained tail on the next inventory refresh. HUD polling never deletes
hook state.

Terminal content stays in bounded HUD memory and is never written to hook
state, trace files, snapshot JSON, or a transcript store. Every captured or
configured display string is sanitized before model state: printable text and
passive SGR styling may remain, while clipboard/title/hyperlink, cursor,
screen-control, string-control, bidi-control, invalid UTF-8, and neighboring
cell effects are neutralized. Rendering resets style at every line and cell
boundary.

Press Enter or choose through `/` to switch. Dispatch uses hidden stable IDs and
revalidates the exact session, window, pane, and pane PID immediately before the
tmux mutation. A malformed, moved, reused, or vanished identity fails closed;
visible labels and indexes are never fallbacks. Use `agents --picker` or
`popup agents --picker` when only fuzzy search and switch are needed.

In the main palette, panes in windows with `automatic-rename` disabled are
shown as `<window name> | <pane title>`. The window name repeats for split panes
while each pane keeps its own title. Automatically renamed windows retain the
existing pane-title-only display.

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

Projects with a different task model can replace that picker locally:

```toml
[status]
command = 'python3 "$TMUX_MENU_SESSION_PATH/scripts/todo.py"'
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
GOCACHE=/tmp/tmux-menu-go-build make validate-config
```

Broader local check:

```sh
GOCACHE=/tmp/tmux-menu-go-build make validate
```
