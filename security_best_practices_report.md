# Security and performance review — 2026-09-07

The verified findings below are fixed locally, with regression tests. This is
a source review and targeted runtime assessment, not a guarantee that every
possible vulnerability or performance problem has been found.

Scope: CLI dispatch and shell templates, fzf framing and previews, config and
project-file reads, status reporter execution, agent preview identity, and
existing hook/HUD boundaries. The pre-existing agent-discovery changes and
concurrent edits to validation guidance were preserved. No installation,
commit, push, or live user-session mutation was performed.

## High severity

### 1. Shell execution through a quoted preview placeholder — fixed

- Rule: GO-INJECT-002.
- Location: `internal/picker/fzf.go:149`, `previewCommandForField`.
- Evidence: the original implementation replaced `{}` directly with `{2}`.
  A configured command such as `cat "{}"` therefore placed fzf's quoted
  filename inside another quoted shell expression. The regression test
  demonstrated that a filename containing `$(touch <temporary marker>)`
  executed the substitution before the fix.
- Impact: a crafted project filename could execute commands as the local user
  when previewed with a double-quoted placeholder. The default `glow` command
  without an explicit placeholder did not have this particular vulnerability.
- Fix: pass the selected field as a separate argument to `sh -c`; use `$1`
  within the configured template, disable splitting/globbing, preserve field
  whitespace, and reject single-quoted placeholders in config validation.
- Validation: `TestPreviewQuotedPlaceholderTreatsPathAsData`; real fzf preview
  of `$(touch PWNED).md` displayed its contents without creating the marker.
- Basis: [fzf's preview placeholder rules](https://github.com/junegunn/fzf/blob/master/man/man1/fzf.1).

## Medium severity

### 2. Untrusted text could corrupt picker records and terminal display — fixed

- Rule: terminal-output boundary; GO-INJECT-002.
- Locations: `internal/picker/fzf.go:73`, `internal/displaytext/text.go:13`,
  `cmd/tmux-menu/status_radar.go`, `statusRadarPreview`.
- Evidence: raw labels and hidden preview fields were joined with tabs and
  newlines. A label containing a newline and numeric ID could create a forged
  row. Reporter details and flat agent capture output were printed without
  filtering active terminal controls.
- Impact: misleading selections/previews and terminal control injection from
  filenames, pane labels, or reporter output. This is a local display boundary,
  not a remote service or authentication bypass.
- Fix: sanitize labels, generated previews, and CLI errors; retain only
  printable text and passive styling. Reject control-bearing preview paths
  without rewriting the action target, and reject malformed multi-row output.
- Validation: picker framing tests, terminal-control tests, Unicode tests,
  and 290,003 sanitizer fuzz executions without a failure.

### 3. Reporter output limits and timeouts did not contain real subprocesses — fixed

- Rule: GO-CONC-001; resource exhaustion.
- Locations: `cmd/tmux-menu/status_radar.go:33`, `statusRadarOutput`, and
  `cmd/tmux-menu/status_radar.go:182`, `runStatusReporter`.
- Evidence: embedding `bytes.Buffer` exposed its `ReadFrom` method, allowing
  subprocess pipe copying to bypass the custom limited `Write`. A 30 ms
  timeout with `sleep 2 & wait` took about 2.01 seconds before the fix because
  a descendant held the output pipe open.
- Impact: an accidental or malicious reporter could exhaust memory or keep
  the entire status view waiting past its configured timeout.
- Fix: private buffer storage, immediate cancellation on stdout overflow,
  a dedicated process group, group cleanup after execution, and a 100 ms
  pipe-drain limit. Both stdout and stderr now traverse the bounded writer.
- Validation: real subprocess overflow and inherited-pipe timeout regressions;
  the timeout test now completes in about 30 ms, and overflow stops in about
  10 ms. The full race suite passes. These are focused regression timings,
  not an application-wide benchmark.
- Basis: [Go subprocess cancellation and WaitDelay](https://pkg.go.dev/os/exec#Cmd).

### 4. A stale flat Agents preview could capture a replacement pane — fixed

- Rule: project stable-identity and terminal-observation boundaries.
- Location: `cmd/tmux-menu/agent_hook.go:840`, `runAgentPreview`, and
  `cmd/tmux-menu/agent_hook.go:878`, `agentPreviewIdentityLive`.
- Evidence: the frozen picker metadata identified a preview only by pane ID;
  capture did not compare its current pane PID/session/window incarnation.
- Impact: a restarted pane could display output belonging to a different
  process than the agent selected in the picker.
- Fix: freeze the session/window/pane/PID tuple, require one exact live match
  before and after capture, and discard output when identity changes. Capture
  is capped at 256 KiB under a 900 ms preview deadline. Hook diagnostic command
  output is also bounded. Terminal output remains memory-only.
- Validation: regressions replace the pane before capture and during capture;
  neither case exposes replacement output. Existing HUD and hook tests pass.

### 5. Project discovery could read arbitrarily large files or block on FIFOs — fixed

- Rule: bounded parsing and resource exhaustion.
- Locations: `internal/fileutil/read.go:32`, `internal/config/config.go:402`,
  `cmd/tmux-menu/bookmarks.go:198`, `cmd/tmux-menu/tools.go:129`,
  `cmd/tmux-menu/status.go:210`.
- Evidence: configuration, Markdown bookmarks, and Makefiles used unbounded
  `os.ReadFile`; task metadata searched an entire file when no summary existed.
- Impact: large project files could stall menus and allocate excess memory;
  a FIFO substituted for a scanned file could block an ordinary open.
- Fix: nonblocking regular-file checks; 1 MiB config/discovery limits;
  64 KiB title/summary reads; overlapping bookmark-root deduplication; ignored
  task-directory pruning; dependency-directory defaults; a 10,000-Markdown-file
  ceiling per root. Config oversize is an error; oversized bookmark/Makefile
  discovery inputs are skipped.
- Validation: sparse 32 MiB files, FIFO rejection, supported regular symlinks,
  exact-size reads, overlapping roots, and metadata beyond the prefix limit.

### 6. Legacy status mock used unsafe scratch and preview paths — fixed

- Rule: GO-INJECT-002; temporary-file integrity.
- Location: `scripts/status-new-mock.sh`, `seed_demo` and fzf invocation.
- Evidence: demo files were written below the predictable
  `tmux-menu-status-new-demo` directory; the preview interpolated `$0` into
  shell code without quoting. The file list also used newline framing.
- Impact: a planted symlink at a demo filename could overwrite another file;
  shell syntax in a checkout/script path could affect preview execution.
- Fix: a private `mktemp -d` scratch directory with cleanup, a quoted script
  path passed through the environment, control-bearing filename exclusion,
  metadata control filtering, and bounded summary reads.
- Validation: `TestLegacyStatusMockUsesPrivateScratch` protects a planted
  symlink target and checks cleanup/template quoting. A separate shell smoke
  check verified hostile filename exclusion. `sh -n` passed.

## Low severity and workflow correctness

### 7. Option-like Makefile targets — fixed

- Rule: GO-INJECT-002, argument validation.
- Location: `cmd/tmux-menu/tools.go`, `validMakeTarget`.
- Evidence: names beginning with `-` passed the character allowlist and were
  appended as Make arguments. They could be interpreted as flags.
- Fix/validation: omit such target names; `TestMakeTargetsCannotBecomeOptions`.
  Make recipes remain deliberately executable project code.

Failed reporters now retain navigation to their live tmux session. Unicode
truncation no longer cuts a multibyte character. New workflow features are:

- Docs picker with title/path search, previews, source deduplication, and
  project-relative configuration.
- Alt-7 links, Alt-8 docs, and Ctrl-R refresh across all fzf views.
- Ctrl-Y copy-only actions for links, bookmarks, docs, and task files.
- A wrapped shortcut footer that stays readable beside the right preview.

## Validation and limits

Passed: `make test`, `make race`, `make vet`, `make build`, `make sample-config`,
and `make validate-config`, all with `GOCACHE=/tmp/tmux-menu-go-build`.
`govulncheck` v1.7.0 reported no vulnerabilities in the final source scan;
`gitleaks dir . --redact` found no secrets in the current checkout.
`git diff --check` passed. The build used Go 1.27.1 on macOS arm64.

The real fzf flow was exercised on a disposable tmux server: docs search and
preview, links/task/docs shortcuts, refreshing edited task metadata, copy-only
dispatch, editor dispatch, and a hostile filename. Clipboard/editor actions
were inspected through the existing dispatch-file boundary. The synthetic
terminal capture is at `/tmp/tmux-menu-docs-screen.txt` for this local run.

User live popups, installed launchers, and provider-hook delivery were not
changed or claimed as newly validated. No end-to-end speedup percentage is
claimed. Configured shell commands, custom preview renderers, Make recipes,
and bootstraps are trusted executable configuration, not sandboxed code.
Filesystem latency and intentionally detached reporter processes are outside
the file-byte/process-group bounds. See [usage](docs/usage.md) for limits and
configuration migration details.
