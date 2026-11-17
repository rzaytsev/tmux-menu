# tmux-menu Usage

## What It Does

`tmux-menu` is a small tmux utility for people who keep multiple projects,
shells, and coding agents open at once. Picker views use `tmux display-popup`
and `fzf --ansi`. Agents uses the same picker interface as the other views.

Views:

- `palette`: switch to configured tmux sessions, panes, and optionally agents.
- `agents`: fuzzy-select live agent panes with status icons and a terminal preview.
- `tools`: run configured commands, Makefile targets, and quick directory jumps.
- `projects`: create or switch tmux sessions for project directories.
- `links`: open URLs and file references captured from pane scrollback.
- `bookmarks`: open typed CommonMark links from configured directories.
- `docs`: find and preview project and notes Markdown files by title or path.
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
tmux-menu popup docs
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
tmux-menu docs
tmux-menu status
tmux-menu validate-config ~/.tmux-menu.conf
tmux-menu sample-config
```

Run a configured command directly by its exact, case-sensitive `title`:

```sh
~/.local/bin/tmux-menu tools dash
```

This loads the same global and local config layers as the Tools picker, applies
the command's `session` filter, and runs its configured mode without opening the
picker. Exactly one matching command is required; missing or duplicate matches
return an error without running anything. Quote titles containing spaces, for
example `tmux-menu tools "Git diff"`. Makefile targets and quick dirs are not
resolved by this form. Running `tmux-menu tools` still opens the picker.

For a configured command with `title = "dash"`, bind prefix + d in tmux:

```tmux
bind d run-shell "~/.local/bin/tmux-menu tools dash"
```

### OSC 7501 status through tmux

Start an agent from its intended tmux pane:

```sh
tmux-menu agent-run codex
tmux-menu agent-run claude
# All following arguments go directly to that provider:
tmux-menu agent-run codex resume
tmux-menu agent-run claude --resume
```

This is opt-in for newly launched sessions. No shell aliases, provider hooks,
tmux configuration, or existing sessions are changed. Run the provider directly
to bypass the wrapper. `agent-run` requires an interactive Unix terminal and
verifies that it actually descends from the tmux pane it claims.

The wrapper creates a PTY and consumes OSC 7501 before tmux can discard it. It
answers the capability probe locally, before forwarding a following DA query;
this enables Claude's native reporting inside tmux. Other terminal output and
keyboard input pass through, window-size changes reach the inner PTY, and the
provider's exit status is preserved. Ctrl-Z suspends the wrapper and child;
`fg` resumes them and restores terminal input. Neither tmux OSC support nor passthrough
configuration is required for collection.

The Agents picker, its two-second preview refresh, and `agent-hook snapshot`
share the same status projection. The native source is `osc7501`:

| Native state | Agents state |
| --- | --- |
| `idle` | `waiting` |
| `working` | `working` |
| `blocked` | `attention`, with permission/question/auth reason |
| `done` | `completed` (a response finished; not proof the task succeeded) |
| `error` | `attention`, with error reason |
| `clear` | Remove the addressed subtree; use remaining records or fallbacks |

Records aggregate in this order: blocked, error, working, done, idle. Child
records can therefore keep an idle parent visibly active or blocked. Unlike
hook leases, native status has no quiet-time expiry. Working/blocked records
clear on an observed shell prompt or provider exit; full terminal reset clears
all records. Done/error records remain in metadata after exit, but only live
agent panes appear in the picker. A restarted provider must establish a new
wrapper/child process identity before native evidence can apply.

Native reports override title/process heuristics and hook state. Codex's native
implementation reports idle instead of done: a hook completion that follows the
latest native work keeps its existing completion badge and exact Ctrl-X token.
Native `done` is cleared by the application's next status report; Ctrl-X still
acknowledges only hook completion tokens. Keep provider hooks configured for
conversation identity, completion, and sessions started without the wrapper.

Storage contains classified status, ids, progress and process identity only.
Messages and titles are validated and discarded; terminal content, prompts,
and model prose never enter OSC state or snapshot JSON. Reports are limited to
4096 bytes and 64 records per wrapped terminal. Snapshot reads require the exact
live pane, wrapper, child and provider process ancestry and do not renew state.

To test manually:

1. Start `tmux-menu agent-run claude` in one tmux pane. In another, run
   `tmux-menu popup agents`; select it and look for `osc7501` in its preview.
2. Submit a harmless request. Expect working while it runs and completed or
   waiting afterward. Cancel a running turn with Escape and check that the
   native status returns to waiting instead of retaining a working hook lease.
3. Exercise a real permission or question prompt and expect attention. Resolve
   or cancel it and check that attention clears. Completion and dialog behavior
   depend on the installed provider's reports.
4. Repeat with `tmux-menu agent-run codex`. Its completed badge may cite hook
   evidence, as explained above. Rows and the selected preview refresh every two
   seconds while idle; Ctrl-R also discovers new agents and reorders the list.
5. For machine-readable confirmation, run `tmux-menu agent-hook snapshot` from
   another pane in the same tmux server and inspect that pane's `source` and
   `status`. Exit the agent and verify that its row disappears.

An automated acceptance test uses an isolated tmux server and a synthetic
provider, without attaching a client, switching your panes, or making API calls:

```sh
GOCACHE=/tmp/tmux-menu-go-build make build
GOCACHE=/tmp/tmux-menu-go-build python3 scripts/test-osc7501.py
# Test an installed binary instead:
GOCACHE=/tmp/tmux-menu-go-build python3 scripts/test-osc7501.py "$HOME/.local/bin/tmux-menu"
```

It checks capability ordering, root/child state transitions, clear/reset,
keyboard input, suspension/resume, resizing, process exit, and shared JSON projection. It requires
Go, tmux and permission to open PTYs and tmux sockets. It does not establish
real-provider approval/cancellation behavior or visual acceptance in your
terminal application.

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

Snapshot v1 also includes `server_id` (fingerprint), `session_id`, `window_id`,
`pane_pid`, and an optional `provider_session_id`. Consumers can match a live
conversation without trusting a historical pane number. Snapshot reads have a
two-second deadline and do not reconcile or renew stored hook state.

`agents` and `agents --picker` open the same fzf picker, including their `popup`
forms. The dashboard has been removed. Existing bindings need no changes.

Rows use aligned STATUS, AGENT, SESSION, AGE, THREAD, WORKDIR, and REASON columns
under a pinned summary and header. Status and agent columns show icons and names.
Long fields truncate, but searches match full titles, session names, paths, and
reasons. Resizing recalculates column widths. The current-pane marker stays
beside the thread title, and `+N` shows child agents there.

AGE means time in the current state, based on a trusted hook transition; renewed
working leases do not reset it. Title/process observations and OSC event times
cannot establish a transition and show `—`. REASON explains attention using the
hook/native reason, or `input required` for title-based attention.

The summary counts each state across this picker generation. `Ctrl-A` toggles
attention, `Ctrl-D` toggles completed, and `Ctrl-S` independently toggles the
origin tmux session. Press a filter shortcut again to clear it. Filters combine
with the text query; empty results retain navigation and filter shortcuts.
`Ctrl-Y` copies the selected agent's full working directory and closes the picker.

Rows update in their original order every two seconds while idle. Updates pause
while typing; the query and selected pane remain stable. `Ctrl-R` discovers new
agents and sorts by urgency again. Exited or changed pane/provider incarnations
disappear rather than becoming new targets. Refresh and view switching retain
each view's own query and selected target, including popup width changes. This
state lasts only for the current popup and is removed when it closes.
Inventory preparation runs in the background; typing cancels pending timed work.

Five preview lines stay pinned above scrollback: status evidence, full thread
title, full workdir, Git branch (or detached commit), and session/state age.
Long details wrap. Git inspection is read-only and bounded; unavailable metadata
shows `—`. Conversation identity and child evidence appear below those details.

Select an agent and look at the single colored line pinned above its preview:

`claude · working · fresh · 6s ago · hook: tool-batch-finished`

Provider and state use your configured agent colors. Defaults are orange Claude,
blue Codex, green working, red attention, cyan completed, and yellow waiting.
The evidence source is cyan; freshness, ages, and separators are dim. Stale or
expired hook evidence is yellow. Long headers truncate to the preview width
instead of wrapping. The preview scroll counter is hidden to keep this line
clear. Hook reasons are preferred over redundant raw event names.

Fallback sources appear as `terminal title` or `process fallback`. Expired
working/waiting hook evidence appears beside the current source, for example:
`terminal title · hook expired: tool-start (3m ago)`.
That event age is historical, not a current turn or state timer. Fresh title or
process observations do not imply that provider hooks are working. The selected
preview refreshes status, evidence, and scrollback every two seconds without
changing the search or selection. The list statuses and summary also refresh
while idle. `Ctrl-R` rediscovers agents, keeping the query and selected pane
when it exists.

Claude's generated `Notification` matcher includes
`permission_prompt|elicitation_dialog`. Merge that matcher into existing
tmux-menu hook entries when upgrading, preserving unrelated hooks, then start a
new Claude session. An MCP elicitation form's notification maps to red
`attention` with `hook: elicitation-dialog`. The notification normally arrives
after about six seconds without typing, rather than immediately when the form
opens. An ordinary `AskUserQuestion` prompt uses the existing `structured-input`
reason. A notification without a correlation ID remains conservative: unrelated
tool progress does not clear attention; a new prompt or response completion does.
See the [Claude hook reference](https://code.claude.com/docs/en/hooks#notification).

Inside the Agents picker, type to filter, use arrows to select, and press `Enter`
to switch to the exact live pane. `Esc` cancels and exits. `Ctrl-X` acknowledges
only the selected exact completion; it does not approve an agent prompt.

Inside fzf picker views:

- `Tab`: next view in `picker.tab_order` (multiselect in links/bookmarks)
- `Shift-Tab`: previous view in `picker.tab_order` (select upward in links/bookmarks)
- `Alt-1`: palette
- `Alt-2`: agents
- `Alt-3`: tools
- `Alt-4`: projects
- `Alt-5`: status
- `Alt-6`: bookmarks
- `Alt-7`: links
- `Alt-8`: docs
- `Ctrl-R`: rebuild the current picker from fresh data
- `Ctrl-Y` in links, bookmarks, docs, and the task board: copy the target/path
  and close the picker without opening it
- `Alt-Enter` in links: alternate opener

The navigation footer is hidden; these shortcuts still work.
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
tab_order = ["palette", "agents", "tools", "projects", "status", "bookmarks", "docs"]

[session]
color = "auto"

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
# Set this in a project/session-local .tmux-menu.conf when needed.
jira_base_url = ""

[links.alternate]
key = "alt-enter"
file_command = "open -a TextEdit"
url_command = "open -a Safari"

[links.open]
mode = "popup"
pane_side = "right"

[bookmarks]
# Omit dirs for session workspace; [] disables scanning.
# dirs = ["docs/bookmarks"]
ignore_patterns = [".git", ".tmp", "vendor", "node_modules", ".venv"]

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
  `bookmarks`, and `docs`. The default omits `links`; `Alt-7` reaches it directly.

### `session`

- `color`: bold session-label color in agent rows. The default, `auto`, assigns
  a stable color based on the session name, shared across panes and refreshes.
  Explicit global or session-local colors override automatic colors. Other values are
  `default`, `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`,
  `bright_black`, `bright_red`, `bright_green`, `bright_yellow`, `bright_blue`,
  `bright_magenta`, `bright_cyan`, `bright_white`, and `orange`.
  `default` uses the terminal's foreground color.

Put this setting in the `.tmux-menu.conf` at a tmux session root to give that
session its own label color:

```toml
[session]
color = "red"
```

### `agents`

`agents.popup_width` controls the Agents picker popup width; the default is
`100%`. Switching views relaunches the popup only when its width changes.

`[agents.icons]` configures the symbols in agent rows:
`codex`, `claude`, `other`, `current`, `branch`, `last_branch`, `attention`,
`working`, `completed`, `waiting`, and `unknown`. An empty `other` keeps the
detected product name.

`[agents.colors]` configures `codex`, `claude`, `other`, `branch`, `thread`,
`workdir`, `attention`, `working`, `completed`, `waiting`, and `unknown`. Colors
accept every explicit `session.color` value plus `dim`; `auto` applies only to
session names.

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

- `title`: display name and exact name for `tmux-menu tools <name>`.
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

`links` reads 300 lines of joined scrollback from the invoking pane and extracts
configured URL schemes, existing file/directory references and optional uppercase
Jira keys. Relative paths use that pane's working directory. Quote or angle-wrap
paths with spaces in prose, or put a path on its own line.

```toml
[links]
url_schemes = ["http", "https", "slack", "tg"]
jira_base_url = "https://dentiai.atlassian.net"
```

Scheme names are case-insensitive; local lists replace inherited lists.
`jira_base_url` turns keys such as `INF-234` into canonical `/browse/INF-234`
URLs. Captured full URLs retain their queries/fragments. Alt-Enter retains the
configured `links.alternate` opener (file/URL command, optional `{}` placeholder).

Both pickers classify targets as `web`, `slack`, `jira`, `notion`, `gh`, `pr`,
`issue`, `ci`, `commit`, `release`, `GCP`, `AWS`, `file`, or `dir`. Classification
uses parsed hostnames with domain boundaries; lookalike domains remain `web`.
GitHub subtypes distinguish repositories, pull requests, issues, Actions runs,
commits and releases. Slack web links and `slack://` are `slack`; Notion includes
`app.notion.com` and `notion.so`. Jira issue URLs use Atlassian `/browse/` paths.

Rows show aligned `type | target` columns in Links and `type | title | target`
in Bookmarks. Filesystem-root (`/`) directory rows are omitted. Titles use Unicode display widths and truncate at a fixed column
width. Long targets stay intact behind fzf's single-line clipping; opening,
copying and preview always receive the complete resolved target.

| Key | Action |
| --- | --- |
| Enter | Open the current row or all Tab-selected targets. |
| Tab / Shift-Tab | Toggle selection and move down / up. |
| Ctrl-Y | Copy only targets, separated by newlines; close the picker. |
| Ctrl-/ | Toggle local file/directory preview; web previews never fetch pages. |
| Alt-F / Alt-D | Show only files / directories. |
| Alt-G | Show all GitHub URLs, regardless of subtype or bookmark title. |
| Alt-S | Show Slack targets. |
| Alt-A | Restore all types. |
| Ctrl-R | Rescan the source. |

Type filters replace the current query. Alt-number view shortcuts remain
available; Tab selects multiple targets in these two views.

Web URLs use the top-level `browser` setting, inherited from home and overridden
by workspace/origin config. An empty browser uses the system default. `slack://`
opens native Slack. File targets open Neovim in a new pane to the right of the
invoking pane. Directories open a new window with an interactive shell in that
directory. Dispatch validates the exact invoking session/window/pane/PID tuple;
the destination retains focus after the popup closes. The popup chooses an
attached client in the invoking session. If several clients share that session,
pass `TMUX_MENU_ORIGIN_CLIENT` (tmux `#{client_name}`) to disambiguate. Enter leaves the clipboard
unchanged. The old `links.open` and `bookmarks.open` fields remain accepted for
config compatibility; typed local targets use the pane/window behavior above.

Locations support `file.md`, `file.md:5`, `file.md:5-10`, `file.md:5:12`,
`file.md#L5`, and `file.md#L5-L10`, plus absolute paths, `~/…`, and local
`file://` URLs. Ranges select lines in Neovim. Resolved targets deduplicate in
source order, retaining a useful title. Queries, fragments and shell characters
in web URLs stay literal; targets are passed as data rather than shell code.

### `bookmarks`

`bookmarks` parses actual CommonMark links from regular `.md` files (including
uppercase `.MD`), recursively, in directory/file/document order. Inline links,
reference links (including collapsed/shortcut forms) and angle autolinks are
supported. Code blocks, inline code, standalone images, bare URL text and raw
HTML links are excluded. An outer link around an image remains a bookmark.

```markdown
[Pull request](https://github.com/org/repo/pull/76)
[Runbook][ops]

[ops]: https://example.com/runbook

<https://example.com/status>
[Source](../src/main.tf:5:12)
[Range](../src/main.tf#L5-L10)
[Notes with spaces](<../docs/design notes.md>)
[Directory](../docs/)
```

Local destinations resolve relative to the containing Markdown file. Local
heading/query suffixes are stripped; line anchors retain navigation. Missing
local targets remain visible and report an error on opening.

Home configuration lives in `~/.tmux-menu.conf`. A workspace list in the session
root's `.tmux-menu.conf` replaces the entire home list; an origin-directory
overlay can replace it again. Lists never merge. Relative directories start at
the directory containing the config that supplied the list; absolute and `~/`
paths work. `dirs = []` disables scanning. Without any list, scan the session
workspace, falling back to `~/notes` if no workspace is available. Invalid
configuration, missing directories and non-directory roots report errors rather
than triggering a broader scan. Overlapping directories are visited once.
`{project}` expansion and `ignore_patterns` retain their existing meanings;
default ignored path parts are `.git`, `.tmp`, `vendor`, `node_modules`, `.venv`.

```toml
# ~/.tmux-menu.conf — paths relative to home
[bookmarks]
dirs = ["notes/bookmarks"]
```

```toml
# ~/projects/denti.ai/.tmux-menu.conf
browser = "Google Chrome"
[bookmarks]
dirs = ["docs/bookmarks"] # must exist; replaces the home list
```

```toml
# ~/projects/clos/.tmux-menu.conf
browser = "Firefox"
```

These settings are independent of `.rex-menu.conf`; no Rex runtime is required.

### `docs`

`tmux-menu popup docs` lists `.md` files from `docs.dirs` in configured source
order, with a file's first `# Heading` as its title and a filename fallback.
The title and full path are searchable. Duplicate files from overlapping roots
appear once. Relative directories resolve from the tmux session root, falling
back to the origin pane directory; `{project}` expands to the project name.

```toml
[docs]
dirs = ["~/notes/projects/{project}", "."]
ignore_patterns = [".git", ".tmp", "vendor", "node_modules", ".venv"]
preview_command = "glow"

[docs.open]
mode = "pane"
pane_side = "right"
```

Press Space to toggle the preview, Enter to open with `editor.command`, or
Ctrl-Y to copy the absolute path for an agent prompt. `docs.open` supports the
same pane, popup, and window modes as bookmarks. Set `dirs = ["./docs", "./todo"]`
in a project overlay to focus on its runbooks and tasks. The picker does not
require a document to contain a Markdown link.

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

## Display and scanning limits

Picker labels, generated radar previews, and agent previews allow printable
text and passive ANSI styling. Terminal commands and bidirectional overrides
are removed. Raw preview paths remain unchanged; paths containing control
characters (including tabs/newlines) fail with an explicit error.

Preview commands accept `{}` unquoted or double-quoted. tmux-menu passes the
selected target as a separate shell argument, preserving quotes and whitespace.
Single-quoted placeholders are rejected by config validation. Custom preview
commands are executable configuration; use a renderer such as `glow` for task
or document content you do not trust.

Config files are limited to 1 MiB and must resolve to regular files. Bookmark
Markdown files and Makefiles larger than 1 MiB are skipped. Document titles
and task summaries read only the first 64 KiB. Keep `summary:` near the top of
task files. Markdown scans stop with an error above 10,000 files per root;
narrow directories or add ignore patterns if needed. Bookmark/document defaults
exclude `.git`, `.tmp`, `vendor`, `node_modules`, and `.venv`; existing explicit
ignore lists continue to replace defaults. Ignored task directories are pruned.

Radar reporters stop on stdout overflow and their process groups are cleaned
up when they finish or time out. A failed reporter remains an `unknown` row,
and Enter can still reach its live session to investigate. Missing sessions
remain display-only. The flat Agents preview checks the exact session, window,
pane and pane PID before and after capture, with a 900 ms deadline and a
256 KiB capture limit. A restarted pane requires Ctrl-R before previewing it.

Project `.tmux-menu.conf` files, Makefile recipes, bootstraps, and configured
commands execute as your user. Inspect these files before using executable
actions from an unfamiliar project. The menu is not a sandbox for project code.

## Use Cases

### Jump Between Projects

Use `projects` when you want one tmux session per project directory. Add
multiple roots if your code lives in more than one parent directory:

```toml
[projects]
roots = ["~/projects", "~/work"]
```

### Keep Coding Agents Visible

Run `tmux-menu popup agents` or `tmux-menu popup agents --picker` when each
agent has its own tmux pane. Both open the same fzf list with 15 visible rows
when space allows; smaller terminals reserve space for the preview. Attention
rows appear first. Type to filter and select a pane to preview it. Every two
seconds while idle, row statuses and the selected preview refresh without
changing the search, selection, or row order. Press `Ctrl-R` to rediscover and
reorder agents. Use `Ctrl-A` for attention, `Ctrl-S` for the current session,
`Ctrl-D` for completed, and `Ctrl-Y` to copy the selected workdir.

The picker requires fzf 0.74.5 or newer for field-based identity tracking across
live reloads. Matching still uses fzf's fuzzy/extended query syntax on full fields.

After building, run the isolated interface regression without opening your popup
or calling a provider:

```sh
python3 scripts/test-agents-picker.py
```

It checks live identity/selection, disappearing rows, full-title search, refresh,
view switching, filters, Git details, resize, and copy dispatch through a fake
clipboard command.

Status is normalized as `attention`, `working`, `completed`, `waiting`, or
`unknown`. Codex and Claude hook evidence takes precedence only while it still
matches the live tmux server, pane ID/PID, session, provider, provider session,
and provider-process incarnation. Titles and process state provide restricted
fallbacks. Stored hook evidence never creates a row without a live pane.

For Codex, a live `Ready` title or empty spinner marker overrides a parent
`working` hook, even while its lease is fresh. This shows `waiting` with
`terminal title` evidence if the completion hook is missing or delayed.
Fresh working child evidence still keeps the aggregate working; completed and
attention hook states retain their existing behavior. This reconciliation is
read-only and does not acknowledge completion or change stored hook records.

The pinned preview header explains the status source, current hook reason and
age when available, and any expired hook evidence. The remaining preview shows
bounded live scrollback from the selected pane. Capture has a 900 ms deadline
and a 256 KiB limit, and revalidates the exact session/window/pane/PID before
and after capture. A changed identity shows a refresh message instead of output.
Terminal content is sanitized and never added to hook state, trace files,
snapshot JSON, or a transcript store.

Press Enter to switch. Dispatch uses hidden stable IDs and revalidates the exact
session, window, pane, and pane PID immediately before the tmux mutation. A
malformed, moved, reused, or vanished identity fails closed; visible labels and
indexes are never fallbacks. `Ctrl-X` acknowledges only the completion rendered
in the list; refresh with `Ctrl-R` first if a newer completion appears in the
preview.

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

Child-first hook events keep the unobserved parent state `unknown`. Older
child-only records with an empty parent state are normalized on read without
rewriting history; all newly persisted hook records are validated.

### Typed picker acceptance check

After `make build`, `scripts/test-links-picker.py` exercises a real popup in an
isolated tmux server with disposable Markdown/files/directories. It uses real
Neovim, checks Unicode column positions, filters, preview, multiple selections,
clipboard contents, directory cwd/focus and browser argv through stubs. It never
opens external URLs. Run it with Python containing `pyte` (for example in a
temporary venv); the rendered terminal is saved to
`/tmp/tmux-menu-links-rendered.txt`. This validates the local build, independently
of the installed launcher.

## tmux 3.4 pane inventory

Pane discovery uses printable length framing because tmux 3.4 may escape control
separators as octal text. Fields containing colons, newlines and UTF-8 remain
length framed. If an older remote binary reports `malformed pane inventory`,
rebuild and reinstall the current binary on that host.
