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
- `agents`: fzf columns, summary, attention/session/completed filters, and a
  selected-pane preview with full thread/workdir/Git details. `agents --picker`
  is a compatible alias. Show 15 result rows when space allows. Refresh row
  metadata and the selected preview every two seconds while idle, preserving
  order, query, selection, and exact row identity. Ctrl-R rediscovers and reorders.
- `tools`: configured commands, dynamic Makefile targets from the session root/current dir, and quick dirs.
- `projects`: lists one-level directories under `projects.roots`, creates/switches tmux sessions natively, and shows whether the configured bootstrap file exists.
- `links`: captures origin pane scrollback, extracts URL schemes configured by `links.url_schemes` (default HTTP, HTTPS, Slack, and Telegram) plus Jira issue keys when the session-local `links.jira_base_url` is set, copies selected targets, opens file refs through configured `links.open`, opens URLs with `open`, and supports a configured alternate opener.
- `bookmarks`: scans inline Markdown links from `bookmarks.dirs` in configured order, default `["~/notes/projects/{project}", "~/projects/{project}"]`, skips path parts matching `bookmarks.ignore_patterns` defaulting to `.git`, `.tmp`, `vendor`, `node_modules`, and `.venv`, opens HTTP(S) links with `open`, and opens local file links in a right-side editor pane.
- `docs`: scans Markdown titles and paths from `docs.dirs`, default notes for `{project}` plus the session root; Space previews, Enter edits, and overlapping roots deduplicate files.
- `status`: when `status.targets` is configured, concurrently runs one JSON reporter per named active tmux session and shows urgency-sorted project rows with a visible block preview; otherwise it runs optional project-local `status.command` or shows the directory task board from `status.status_dir` and `status.statuses`.
- Picker views support Tab/Shift-Tab through configured `picker.tab_order` and `Alt-1` main, `Alt-2` agents, `Alt-3` tools, `Alt-4` projects, `Alt-5` status, `Alt-6` bookmarks, `Alt-7` links, and `Alt-8` docs through `fzf --expect`; Ctrl-R refreshes every picker. Ctrl-Y copies targets/paths in links, bookmarks, docs, and directory status. The navigation footer is hidden; shortcuts stay active, while `picker.show_help` controls optional view-specific help.
- Global config path: `~/.tmux-menu.conf`; local `.tmux-menu.conf` overlays in the session root and origin pane directory can add commands/quick dirs or replace scalar/list settings.

## Implementation Rules

- Keep using `fzf --ansi` unless a feature clearly requires a custom TUI.
- Do not add Bubble Tea just for colors or simple row layout.
- Keep Agents and other popup views on fzf.
- Keep popup view switching on cheap `fzf --expect` bindings; do not add a custom TUI for simple mode switching.
- Use stable tmux IDs and pane process identity for dispatch. Immediately before
  a pane switch, require one exact live session/window/pane/pane-PID tuple; fail
  closed without names or indexes.
- Agent detection checks command/title first, then descendant processes from `#{pane_pid}`.
- Live tmux panes own agent row existence. Fresh hook evidence may annotate only
  an exact server/pane/PID/session/provider-session/provider-process incarnation
  and must never create a row by itself.
- `agent-run <codex|claude> [args...]` is the opt-in OSC 7501 PTY receiver.
  Preserve non-protocol terminal traffic and capability-reply ordering. OSC
  metadata uses a separate event-based store, never hook TTLs or model prose.
  Require live pane/wrapper/child/provider ancestry before projection.
- Normalize hook state to `attention`, `working`, `completed`, `waiting`, and
  `unknown`. Stop means response completion, not task success; working is a
  renewable lease; polling never renews it.
- A Codex idle title overrides a parent working hook lease unless fresh working
  child evidence keeps the aggregate active. Preserve completed/attention hook
  behavior and report terminal-title evidence without old working clocks.
- Codex agent status reads `#{pane_title}` and `#{window_name}` from Codex `tui.terminal_title`: status item `Ready` is yellow `waiting`, `Working` or `Thinking` is green `working`, `Action Required` is bold red `attention`; spinner-only title markers map braille/dot to `working` and empty marker before `|` to `waiting`.
- Claude Code agent status reads leading `#{pane_title}` markers when present: star-like idle markers are yellow `waiting`, animated spinner markers are green `working`, explicit needs-input/permission text is bold red `attention`.
- Other non-Codex agent status uses process state, not blocking output-delta polling: green `working`, yellow `waiting`, dim `unknown`.
- Agent session-label colors come from `[session].color` in each tmux session root's `.tmux-menu.conf`; supported values cover terminal default, normal and bright ANSI colors, and orange.
- Agent product, current-thread, and status icons come from `[agents.icons]`; their colors plus thread/workdir colors come from `[agents.colors]`. Agent colors support the session color values plus `dim`; Codex defaults to blue and completed to bright cyan.
- Keep one shared semantic inventory projection for picker and snapshot;
  view-specific order and terminal content stay outside it.
- The selected-agent preview freezes pane identity for its picker generation.
  Use fzf timed reloads with field-based identity tracking for live row metadata,
  preserving query, selection, and row order; only manual refresh rediscovers
  and reorders. Do not enable tracking on query reloads, which can drop typing.
  Preserve exact rendered acknowledgement tokens. Use read-only hook
  lookups; polling must not renew leases or reconcile hook records. Pin status
  and evidence in one colored line, truncated to the preview width; pin full
  thread/workdir, read-only Git metadata, and session/state age beneath it. Reuse
  configured provider/status colors. Expired hook reasons are historical only.
- Bound terminal capture and revalidate the exact live pane identity before and
  after capture. Sanitize captured terminal content, labels, icons, and errors;
  allow only printable text and passive allowlisted SGR.
- Terminal previews are display-only data. Never add them to hook state, trace,
  snapshot JSON, or a transcript store.
- `Ctrl-X` retains exact-token completion acknowledgement; it must not approve
  agent prompts or send keys to the agent.
- Tab/Alt-2 enter the Agents picker. Relaunch a popup only when its width changes.
  Both `agents` and `agents --picker` exit on cancellation.
- The dedicated Agents popup width comes from `agents.popup_width`, default
  `100%`.
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
- A non-empty `status.command` replaces the directory picker and runs from the tmux session root with the `TMUX_MENU_*` runtime context.
- Non-empty `status.targets` takes precedence over `status.command`; each target matches an exact active session name, runs one reporter from that session root, and uses the stable session ID for Enter dispatch.
- Status reporters return a JSON object with a non-empty `blocks` array. Blocks require `title`, `status`, and `summary`, may include `details`, and use only `attention`, `warning`, `ok`, or `unknown`.
- Radar reporters run concurrently with the positive `status.report_timeout` and a 1 MiB stdout limit; missing sessions, failures, timeouts, and invalid output remain visible as `unknown` reports rather than aborting the view.
- Radar rows sort by `attention`, `warning`, `unknown`, then `ok`, retain configured target order within each severity, and use a visible fixed right preview with blocks in reporter order.
- Status `statuses` controls the visible status subdirectories and display order; directories not listed, such as `old`, are hidden.
- Directory-board status previews use `fzf --preview` hidden by default and toggled with Space; `preview_command = "glow"` appends the selected file path, while commands containing `{}` replace that placeholder with the file path.
- Status item preview fields must pass raw file paths to `fzf`; do not pre-shell-quote them.
- Sanitize picker labels and generated previews with `internal/displaytext`; reject control-bearing raw preview fields. Pass preview targets separately from shell templates. Bound project-file reads and reject non-regular files through `internal/fileutil`.
- Status labels are only `<status> | <title> | <summary>`; do not include a redundant `status` kind prefix or visible file path.
- Status selections open the file through `editor.command` using configured `status.open`.
- Link file selections use `editor` config for command and size plus configured `links.open`; default is `$EDITOR` in an 80% x 80% rounded popup.
- Jira issue keys are uppercase `<PROJECT>-<NUMBER>` tokens; when `links.jira_base_url` is configured, rows use the `jira` kind, targets are canonical `<base>/browse/<key>` URLs, and captured full Jira URLs deduplicate with issue-key mentions.
- Bookmark local-file selections use `editor.command` and configured `bookmarks.open`; resolve relative links from the source Markdown file and strip local `#fragment`/`?query`.
- Bookmark row kind labels use the configured source directory name, not the literal word `bookmark`.
- Do not show local hostname prefixes in pane titles.
- Palette sections are only `agents`, `sessions`, and `panes`; do not put commands or quick dirs into `palette`.

## Validation

For Go behavior changes, run the affected package/test first, then once:

```sh
GOCACHE=/tmp/tmux-menu-go-build make test
GOCACHE=/tmp/tmux-menu-go-build make build
```

Add `make race` for concurrency or hook state changes. Add
`make sample-config` and `make validate-config` when config parsing, shape,
defaults, or examples change. Use the same `GOCACHE` prefix for these checks.
Documentation-only changes need a diff and affected-link/example review.
Verify a changed picker flow in the real interface when authorized; a
passing build does not establish that the installed launcher uses that build.

Do not run interactive `make palette`, `make agents`, `make tools`, `make projects`, `make links`, `make bookmarks`, `make docs`, or `make status` unless the user explicitly wants the popup opened.

## Docs

When behavior changes, update:

- `README.md` for short project overview
- `docs/usage.md` for operator usage, examples, config reference, and use cases
- `specs.md` for product behavior
- `examples/config.toml` if config shape/defaults change
