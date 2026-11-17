# tmux-menu

Small Go command palette for tmux.

`tmux-menu` opens tmux pickers so you can check coding agents and jump between
sessions, panes, project directories, links, bookmarks, tools, and task files
without leaving the keyboard.
Status can also act as a cross-project radar: configured session reporters
produce urgency-sorted rows with a fixed right-side block preview.
Manually named tmux windows are shown alongside each pane's own title.
The Agents view is a single fzf picker with aligned STATUS, AGENT, SESSION,
AGE, THREAD, WORKDIR, and REASON columns, a pinned summary, and a selected-pane
preview with full thread/workdir details and the Git branch. Search matches full
titles and paths even when columns truncate. Queries and selection survive
refresh and view switching for the lifetime of the popup.
Unconfigured session names get stable automatic colors; `[session].color`
overrides them. `agents` and `agents --picker` open the same interface; the dashboard
has been removed. Every switch revalidates the exact tmux session, window, pane,
and pane-process identity before dispatch. One colored line stays pinned above
the preview, combining provider, status, age, and hook/title/process evidence.
Expired hook evidence stays visibly historical; Claude MCP elicitation dialogs
are marked as needing attention. Rows and the selected preview refresh every
two seconds while idle without reordering agents. The list shows 15 rows when
space allows; `Ctrl-R` rediscovers and reorders agents. `Ctrl-A` filters attention,
`Ctrl-S` toggles the current session, `Ctrl-D` filters completed, and `Ctrl-Y`
copies the selected workdir. AGE uses trusted hook transitions, otherwise `—`.
Codex's live idle title overrides a leftover parent working hook, so a missing
completion event cannot keep an idle agent showing as working.
Start new Codex/Claude sessions with `tmux-menu agent-run codex` or
`tmux-menu agent-run claude` to receive OSC 7501 status through tmux. The
wrapper answers capability probes, forwards the terminal, and labels native
evidence `osc7501` in Agents and JSON snapshots. Existing hooks remain useful
for conversation identity and Codex completion. See
[OSC 7501 setup and testing](docs/usage.md#osc-7501-status-through-tmux).
The read-only `agent-hook snapshot` export includes exact conversation and tmux
identity metadata for notification inboxes that display current agent state.
Agent marks, status icons, and colors remain configurable through
`[agents.icons]`, `[agents.colors]`, and per-session `[session].color`.
Tab and Shift-Tab cycle through a configurable picker order, except Links,
Bookmarks and Completion where they select multiple targets. Alt-number
shortcuts remain available; Completion shows its action/source help.
Links and Bookmarks share a typed, aligned fzf picker with multiselect,
file/directory/GitHub/Slack filters and local previews. Links reads the invoking
pane's scrollback; Bookmarks parses CommonMark links from configured directories.
Files open in a focused Neovim pane with line/column/range navigation; directories
open a focused shell window; filesystem-root (`/`) rows are omitted. Web links use the workspace/home `browser` setting,
and `slack://` opens Slack. See [typed links and bookmarks](docs/usage.md#links).
The docs picker searches Markdown titles and paths across project files and
notes, with Space to preview and Enter to edit. Use `Alt-7` for links, `Alt-8`
for docs, and `Ctrl-R` to refresh any picker. `Ctrl-Y` copies a link, bookmark
target, document path, or task path without opening it.

Completion (`Alt-9`, or `tmux-menu popup complete`) finds files, directories,
changed files, branches, tags, commits, worktrees, and screen references to insert
into a Codex/Claude draft. Tab selects multiple items; Enter pastes literal text
without submitting, Alt-Enter inserts simple `@path` text, Ctrl-S shell-quotes,
and Ctrl-Y copies. It freezes the invoking pane and checks its identity, command,
cwd and bracketed-paste mode before insertion. See [Completion](docs/usage.md#completion)
for provider keys, limits and the optional Prefix+Ctrl-T binding.

## Requirements

- Go
- tmux
- fzf 0.74.5 or newer (for identity tracking across live reloads)
- macOS `open`/`pbcopy` for the default URL and clipboard actions
- optional: `glow` for task and document previews

Pane discovery uses length framing with printable separators for tmux 3.4 compatibility.

## Install

```sh
make build
make install
```

The binary is built at `bin/tmux-menu` and installed to
`~/.local/bin/tmux-menu` by default.

## Usage

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
tmux-menu popup complete
tmux-menu popup complete branches
tmux-menu validate-config ~/.tmux-menu.conf
```

Run the Agents picker without creating a popup (`--picker` is a compatible alias):

```sh
tmux-menu agents
tmux-menu agents --picker
```

Suggested tmux binding:

```tmux
bind -n C-Space run-shell "tmux-menu popup palette"
```

Run a configured command directly by its exact `title`, without the Tools picker:

```sh
~/.local/bin/tmux-menu tools dash
```

For a command with `title = "dash"`, a tmux binding can use:

```tmux
bind d run-shell "~/.local/bin/tmux-menu tools dash"
```

Global config lives at `~/.tmux-menu.conf`. Start from:

```sh
tmux-menu sample-config > ~/.tmux-menu.conf
```

Full usage, configuration, examples, and use cases are in
[docs/usage.md](docs/usage.md).

## Development

```sh
GOCACHE=/tmp/tmux-menu-go-build make test
GOCACHE=/tmp/tmux-menu-go-build make build
GOCACHE=/tmp/tmux-menu-go-build make sample-config
GOCACHE=/tmp/tmux-menu-go-build make validate-config
```

Child-first hook events keep the unobserved parent state `unknown`. Older
child-only records with an empty parent state are normalized on read without
rewriting history; all newly persisted hook records are validated.
