# tmux-menu

Small Go command palette for tmux.

`tmux-menu` opens tmux pickers through `fzf` so you can jump between sessions,
panes, agent panes, project directories, links, bookmarks, tools, and task
files without leaving the keyboard.
Manually named tmux windows are shown alongside each pane's own title.
The agents picker groups panes under display-only session headers and previews
the selected pane's latest scrollback lines, with popup and preview sizing
controlled by the shared config. Selectable tree rows show a compact status
sign, a blue Codex `>` or orange Claude `✳` mark, thread name, and compact
workdir with the common `~/projects/` prefix omitted. Agent marks, tree/status
icons, and their colors are configurable through `[agents.icons]` and
`[agents.colors]`.
Each session-root `.tmux-menu.conf` can set its header `[session].color`.
Tab and Shift-Tab cycle through a configurable picker order, while a persistent
footer shows those controls and the direct Alt-number shortcuts.
The links picker recognizes configurable URL schemes, including HTTP(S), Slack,
and Telegram deep links by default.

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
tmux-menu popup tools
tmux-menu popup projects
tmux-menu popup links
tmux-menu popup bookmarks
tmux-menu popup status
tmux-menu validate-config ~/.tmux-menu.conf
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
