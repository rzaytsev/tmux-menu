# tmux-menu

Small Go command palette for tmux.

`tmux-menu` opens a live agent HUD plus tmux pickers so you can monitor coding
agents and jump between sessions, panes, project directories, links, bookmarks,
tools, and task files without leaving the keyboard.
Status can also act as a cross-project radar: configured session reporters
produce urgency-sorted rows with a fixed right-side block preview.
Manually named tmux windows are shown alongside each pane's own title.
The Agents view is an adaptive live grid: it refreshes visible terminal tails,
keeps stable pane positions, pages beyond four agents, highlights attention,
and expands one selected pane for focused reading. Press `/` for the existing
fzf switcher, or run `agents --picker` directly. Every switch revalidates the
exact tmux session, window, pane, and pane-process identity before dispatch.
Agent marks, status icons, and colors remain configurable through
`[agents.icons]`, `[agents.colors]`, and per-session `[session].color`.
Tab and Shift-Tab cycle through a configurable picker order, while a persistent
footer shows those controls and the direct Alt-number shortcuts.
The links picker recognizes configurable URL schemes, including HTTP(S), Slack,
and Telegram deep links by default. A session-local Jira base URL can also turn
issue keys such as `INF-234` into canonical `jira` browser-link rows.

## Requirements

- Go
- tmux
- fzf
- macOS `open`/`pbcopy` for the default URL and clipboard actions
- optional: `glow` for status-file previews

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
tmux-menu popup status
tmux-menu validate-config ~/.tmux-menu.conf
```

Run the HUD or direct picker without creating a popup:

```sh
tmux-menu agents
tmux-menu agents --picker
```

Suggested tmux binding:

```tmux
bind -n C-Space run-shell "tmux-menu popup palette"
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
