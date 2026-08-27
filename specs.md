# tmux-menu Spec

## Goal

Build a small Go tool for tmux users who keep several project sessions open.
The tool has seven user-facing views:

1. `palette`: fuzzy-select tmux sessions and panes.
2. `agents`: continuously monitor live agent panes and delegate fuzzy switching
   to the existing Agents picker.
3. `tools`: fuzzy-select configured commands and quick dirs.
4. `links`: fuzzy-select file and URL references from the active pane scrollback.
5. `bookmarks`: fuzzy-select Markdown links from project notes and project files.
6. `status`: fuzzy-select task files under a configured status directory.
7. `projects`: fuzzy-select a project and create/switch tmux sessions natively.

The first implementation favors low complexity over custom UI polish. It uses:

- Go for tmux discovery, config parsing, dispatch, and safety.
- Bubble Tea for the persistent multi-pane Agent HUD.
- `fzf` for fuzzy selection inside tmux picker popups.
- `tmux display-popup` as the launcher surface.

## Commands

```sh
tmux-menu popup palette
tmux-menu popup agents
tmux-menu popup agents --picker
tmux-menu popup tools
tmux-menu popup projects
tmux-menu popup links
tmux-menu popup bookmarks
tmux-menu popup status
tmux-menu palette
tmux-menu agents
tmux-menu agents --picker
tmux-menu tools
tmux-menu projects
tmux-menu links
tmux-menu bookmarks
tmux-menu status
tmux-menu validate-config [config ...]
tmux-menu agent-hook doctor
tmux-menu agent-hook snapshot
tmux-menu agent-hook snippets [codex|claude] [ingest|trace]
tmux-menu sample-config
```

Inside the Agent HUD:

- arrows or `h`, `j`, `k`, `l`: move selection
- `[` / `]`: previous/next page
- `n`: next attention item across pages
- `z`: toggle focus
- `?`: toggle full help
- `/`: delegated existing fzf Agents picker
- `Enter`: exact live-pane switch
- `q`: quit

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
- agent session labels: bold, using that session root's configured
  `[session].color` (cyan by default); named options cover terminal default,
  the normal and bright ANSI palettes, and orange
- ordinary pane commands: green
- remote commands such as `mosh` and `ssh`: yellow
- agent product, status, thread, and workdir colors: configured through
  `[agents.colors]`; defaults are blue Codex, orange Claude, magenta other,
  green working, bright-cyan completed, yellow waiting, red attention, and dim unknown/workdir
- metadata: dim

Bubble Tea is used only for the Agent HUD because its persistent live grid,
focus mode, cross-page attention, resize handling, and automatic refresh cannot
be expressed safely as an fzf picker. All fuzzy selection and ordinary popup
view switching remain on `fzf --ansi` and `fzf --expect`.

## Agents Mode

`agents` opens the adaptive Agent HUD. `agents --picker` bypasses it and opens
the existing fzf Agents picker. The popup forms accept the same optional flag.
Entering Agents from palette Tab/Alt-2 relaunches the top-level HUD even when
the old and new popup widths are identical.

Only currently live tmux panes matched by command/title, descendant process, or
fresh exact hook evidence appear. Stored evidence may annotate a live matching
pane but never creates a row. One shared semantic inventory owns pane identity,
provider, normalized status, source, freshness, child rollup, and trusted timing
anchors for the HUD, picker, and metadata-only `agent-hook snapshot`.

The overview shows up to four stable cells and pages additional agents. Status
changes update a cell in place; they do not reorder slots. Narrow layouts reduce
columns and rows until one selected terminal remains. Focus mode dedicates most
of the body to that terminal. The header summarizes every page by the five
non-color-only statuses: `attention`, `working`, `completed`, `waiting`, and
`unknown`. Empty inventory remains open and continues discovery.

Each cell contains provider, status, session, thread, workdir when space allows,
optional trustworthy telemetry, and a bounded terminal tail. The clocks are:

- `turn`: elapsed time since the trusted current turn started
- `state`: time since the normalized state actually changed
- `event`: age since the last trusted provider event

Fallback-only and legacy rows omit unavailable clocks. Child count is shown
only from fresh metadata. V1 does not infer token cost, context utilization,
task percentage, tool counts, or changed-file ownership.

Refresh generations are serial and observational. A measured disposable tmux
socket baseline completed inventory and styled capture below the timer's 0.01s
resolution; v1 therefore uses a conservative 900 ms generation deadline and
schedules the next refresh about one second after completion. A generation
captures only the current page (maximum four panes) or the focused pane, with
200 lines and 256 KiB per pane, a 13 MiB shared output budget (4 MiB pane
inventory + 4 MiB process inventory + 4 MiB post-capture identity validation +
4*256 KiB captures), and a 256 KiB retained terminal budget. Hook and config
file reads are regular-file bounded and charged to the same budget. Hidden
pages retain no terminal content. Capture failure preserves the last safe
terminal with a stale marker; inventory failure marks the generation stale;
pane exit or incarnation change removes the row and its tail on the next
successful inventory refresh. A post-capture pane-ID/PID check rejects output
from an identity reused during capture. Polling never renews hook leases,
deletes hook state, or changes agent lifecycle state.

All pane output, labels, errors, and configured display values are untrusted.
Before model state, tmux-menu bounds and sanitizes them into printable spans with
only passive allowlisted SGR styling. OSC clipboard/title/hyperlink operations,
DCS/APC and tmux passthrough, non-SGR CSI, cursor/screen controls, C0/C1 and bidi
controls, malformed sequences, and invalid UTF-8 cannot survive as active
controls. Renderer-owned resets terminate every line and cell. Terminal output
is retained only in bounded HUD memory and is never written to hook state,
trace, snapshot JSON, or a transcript store.

`/` suspends the HUD and runs the existing fzf picker. Cancel resumes the same
selection, page, and focus if its pane remains live; direct picker cancel exits.
The delegated picker retains `Ctrl-R`, `Ctrl-X`, fuzzy selection, and view
switches. HUD-native keys cannot send keys, approve input, acknowledge
completion, ingest hooks, or otherwise mutate an agent.

Enter and picker selection carry stable session, window, pane, pane PID, and
provider PID metadata internally. Immediately before tmux mutation, dispatch
lists live panes and requires exactly one canonical session/window/pane/pane-PID
tuple. Malformed, moved, reused, vanished, or unavailable inventory fails closed
without label or index fallback.

Codex titles still map `Action Required` to attention, `Working`/`Thinking` and
spinner markers to working, and `Ready`/empty spinner markers to waiting. Claude
idle markers map to waiting, animated markers to working, and explicit
needs-input/permission to attention. Fresh identity-bound hook state can add
completed and authoritative attention/working/waiting; other agents use process
working/waiting/unknown fallback. The direct fzf picker preserves the configured
icons/colors, compact labels, exact completion acknowledgement, and live
selected-pane preview.

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

When `status.targets` is non-empty, `status` shows one row per configured active
tmux session. Each target defines an optional title, an exact session name, and
one reporter command. Commands run concurrently from their target session roots
with a per-command `status.report_timeout`, default `3s`, and receive target
session values through `TMUX_MENU_SESSION_*` plus the caller pane/path through
`TMUX_MENU_ORIGIN_*`.

Reporter stdout is a JSON object with a non-empty `blocks` array. Each block has
required `title`, `status`, and `summary` strings plus optional `details`.
Supported block statuses are `attention`, `warning`, `ok`, and `unknown`.
Unknown fields, malformed JSON, invalid blocks, process failures, and timeouts
produce a visible `unknown` report rather than aborting the whole radar.
Reporter stdout is limited to 1 MiB.

Radar rows are ordered by aggregate severity (`attention`, `warning`, `unknown`,
`ok`) and retain configured target order within a severity. tmux-menu derives the
aggregate state and top two problem summaries. A fixed, visible right preview
shows blocks in reporter order. Enter switches to an available target through
its stable tmux session ID; missing configured sessions remain visible and are
display-only.

When no targets are configured and `status.command` is non-empty, `status` runs
that shell command from the tmux session root with the `TMUX_MENU_*` context
variables and does not open the directory picker. This lets project overlays
supply a task workflow with a different storage model.

Otherwise, `status` shows a lane-ordered task board from each
`status.status_dir`. Relative
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

[agents]
popup_width = "100%"

[agents.icons]
codex = ">"
claude = "✳"
current = "*"
working = "●"
completed = "✓"
waiting = "○"

[agents.colors]
codex = "blue"
claude = "orange"
working = "green"
completed = "bright_cyan"
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
# command = 'python3 "$TMUX_MENU_SESSION_PATH/scripts/todo.py"'
report_timeout = "3s"
status_dir = ["./todo"]
statuses = ["new", "doing", "done"]
preview_command = "glow"
ignore_patterns = [".gitkeep"]

# [[status.targets]]
# title = "Example"
# session = "example"
# command = "./scripts/status-report"

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
./tmux-menu validate-config ~/.tmux-menu.conf
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
