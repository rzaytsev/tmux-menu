# AGENTS.md

Work directly and keep changes small. This is a local tmux utility, not a framework.

## Project Shape

- Go module at repo root.
- Main CLI: `cmd/tmux-menu/main.go`.
- tmux wrapper logic: `internal/tmux`.
- dispatch actions: `internal/action`.
- config model/defaults: `internal/config`.
- fzf integration: `internal/picker`.
- Example user config: `examples/config.toml`.
- Product behavior/spec: `specs.md`.

## Current Behavior

- `palette`: configured sections from `palette.sections`; default sessions, then panes. No window rows.
- `agents`: dedicated agent-pane picker grouped as a tree by tmux session. Session headers use per-session colors and are display-only; selectable child rows show status, a Codex/Claude mark, thread name, and workdir without window/pane indexes or a leading `~/projects/`. The current agent thread name is prefixed with `*`.
- `tools`: configured commands, dynamic Makefile targets from the session root/current dir, and quick dirs.
- `projects`: lists one-level directories under `projects.roots`, creates/switches tmux sessions natively, and shows whether the configured bootstrap file exists.
- `links`: captures origin pane scrollback, extracts URL schemes configured by `links.url_schemes` (default HTTP, HTTPS, Slack, and Telegram), copies selected targets, opens file refs through configured `links.open`, opens URLs with `open`, and supports a configured alternate opener.
- `bookmarks`: scans inline Markdown links from `bookmarks.dirs` in configured order, default `["~/notes/projects/{project}", "~/projects/{project}"]`, skips path parts matching `bookmarks.ignore_patterns` defaulting to `.git`, `.tmp`, and `vendor`, opens HTTP(S) links with `open`, and opens local file links in a right-side editor pane.
- `status`: shows a task board from configured `status.status_dir` roots, default `["./todo"]`, and configured `status.statuses` subdirs, default `["new", "doing", "done"]`; rows show status, filename-derived title with `-`/`_` as spaces, and `summary: ...`, with Space toggling full preview and Enter opening the file in an editor split pane.
- Picker views support Tab/Shift-Tab through configured `picker.tab_order` and `Alt-1` main, `Alt-2` agents, `Alt-3` tools, `Alt-4` projects, `Alt-5` status, `Alt-6` bookmarks through `fzf --expect`; navigation shortcuts are always shown in a persistent footer, while `picker.show_help` controls optional view-specific help.
- Global config path: `~/.tmux-menu.conf`; local `.tmux-menu.conf` overlays in the session root and origin pane directory can add commands/quick dirs or replace scalar/list settings.

## Implementation Rules

- Keep using `fzf --ansi` unless a feature clearly requires a custom TUI.
- Do not add Bubble Tea just for colors or simple row layout.
- Keep popup view switching on cheap `fzf --expect` bindings; do not add a custom TUI for simple mode switching.
- Use stable tmux IDs for dispatch: session/window/pane IDs, not names/indexes.
- Agent detection checks command/title first, then descendant processes from `#{pane_pid}`.
- Codex agent status reads `#{pane_title}` and `#{window_name}` from Codex `tui.terminal_title`: status item `Ready` is yellow `waiting`, `Working` or `Thinking` is green `working`, `Action Required` is bold red `attention`; spinner-only title markers map braille/dot to `working` and empty marker before `|` to `waiting`.
- Claude Code agent status reads leading `#{pane_title}` markers when present: star-like idle markers are yellow `waiting`, animated spinner markers are green `working`, explicit needs-input/permission text is bold red `attention`.
- Other non-Codex agent status uses process state, not blocking output-delta polling: green `working`, yellow `waiting`, dim `unknown`.
- Agent session-header colors come from `[session].color` in each tmux session root's `.tmux-menu.conf`; supported values cover terminal default, normal and bright ANSI colors, and orange.
- Agent product, current-thread, tree, and status icons come from `[agents.icons]`; their colors plus thread/workdir colors come from `[agents.colors]`. Agent colors support the session color values plus `dim`; Codex defaults to blue.
- Keep command modes explicit: `popup`, `paste`, `window`, `tmux`, `shell`.
- Keep quick dirs separate from generic commands, but show commands, Makefile targets, and quick dirs only in `tools`.
- `commands[].session` filters commands to a matching tmux session, same matching behavior as `quick_dirs[].session`.
- Makefile targets are simple parsed target names from `Makefile` only, ignore variable assignments, and dispatch as `make -C <dir> <target>` pasted with Enter into the origin pane; home paths should use `$HOME/...`.
- Project bootstrap visibility uses `projects.bootstrap_file`, default `.tmux-sessionizer`.
- Project roots use `projects.roots`, default `["~/projects"]`; support multiple roots such as `["~/projects", "~/code"]`.
- Project selection must not depend on `~/.scripts/tmux-sessionizer`; native dispatch creates/switches tmux sessions and runs the bootstrap file only when a session is newly created.
- Relative quick-dir paths resolve against `#{session_path}`; use `quick_dirs[].session` for project-specific entries, and `quick_dirs[].command` to paste `cd <path> && <command>`.
- In popup-launched views, use `TMUX_MENU_ORIGIN_PANE` and `TMUX_MENU_ORIGIN_PATH` when inspecting the caller pane.
- Status `status_dir` values are arrays and are resolved from `TMUX_MENU_SESSION_PATH` / `#{session_path}`, with origin path only as fallback.
- Status `statuses` controls the visible status subdirectories and display order; directories not listed, such as `old`, are hidden.
- Status previews use `fzf --preview` hidden by default and toggled with Space; `preview_command = "glow"` appends the selected file path, while commands containing `{}` replace that placeholder with the file path.
- Status item preview fields must pass raw file paths to `fzf`; do not pre-shell-quote them.
- Status labels are only `<status> | <title> | <summary>`; do not include a redundant `status` kind prefix or visible file path.
- Status selections open the file through `editor.command` using configured `status.open`.
- Link file selections use `editor` config for command and size plus configured `links.open`; default is `$EDITOR` in an 80% x 80% rounded popup.
- Bookmark local-file selections use `editor.command` and configured `bookmarks.open`; resolve relative links from the source Markdown file and strip local `#fragment`/`?query`.
- Bookmark row kind labels use the configured source directory name, not the literal word `bookmark`.
- Do not show local hostname prefixes in pane titles.
- Palette sections are only `agents`, `sessions`, and `panes`; do not put commands or quick dirs into `palette`.

## Validation

Run:

```sh
GOCACHE=/tmp/tmux-menu-go-build make test
GOCACHE=/tmp/tmux-menu-go-build make build
GOCACHE=/tmp/tmux-menu-go-build make sample-config
```

Do not run interactive `make palette`, `make agents`, `make tools`, `make projects`, `make links`, `make bookmarks`, or `make status` unless the user explicitly wants the popup opened.

## Docs

When behavior changes, update:

- `README.md` for short project overview
- `docs/usage.md` for operator usage, examples, config reference, and use cases
- `specs.md` for product behavior
- `examples/config.toml` if config shape/defaults change
