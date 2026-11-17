# Completion for agent drafts

## Research

- [tmux-fzf-complete](https://github.com/junegunn/tmux-fzf-complete): pane-cwd files/directories plus screen URLs, paths, sentences; multiselect, preview, copy, open, refresh and provider switching. Guesses a preceding word and replaces it.
- [fzf-git.sh](https://github.com/junegunn/fzf-git.sh): files, branches, tags, remotes, commits, stashes, reflogs, worktrees and refs; tmux bindings work outside a shell prompt.
- Current tmux-menu already has fzf multiselect, private popup dispatch, exact pane identity validation, bounded file readers, typed scrollback links and sanitized previews. Extend these rather than install Ruby/shell plugins as dependencies.
- Live local versions: tmux 3.8-rc, fzf 0.75.0. tmux-fzf-complete is already configured via TPM. Preserve existing unrelated dirty work.

## Observable success

From a disposable tmux pane running an agent-like input program, open Completion, find a file with spaces, select several paths or Git references, and insert into the existing draft at its cursor. No Enter, draft deletion, command execution, or input to another pane. Copy and cancellation leave the draft alone. A replaced/moved origin pane fails closed. Install the tested binary and persistent bindings, checking the saved and live commands.

## Proposed scope

1. Add `complete` / `popup complete [provider]`, reachable from every picker with Alt-9; optionally allow it in tab order, retaining existing default order. Reuse fzf and popup dispatch, with Tab selecting multiple rows. Show provider shortcuts and insertion actions.
2. Providers: files, dirs, changed files, branches (local and remote), tags, commits, stashes, remotes, worktrees, and screen references (URLs/local paths) plus screen text. Default to files rooted at the invoking pane's current directory. Git file listing respects ignores and includes untracked files. Outside Git use a bounded traversal pruning VCS, dependencies and caches. Keep files under pane cwd; Git objects use the enclosing worktree. Explicitly display empty/error states and limits. No network calls or Git mutation.
3. Enter inserts plain text. Alt-Enter inserts explicit `@path` references for files/dirs (quote whitespace), Ctrl-S shell-quotes each selected value, Ctrl-Y copies. Preserve raw values separately from sanitized labels/opaque preview tokens. Never infer provider-specific editing keys, delete a word, or submit the draft. Provider-specific @ interpretation remains user acceptance, not a claimed integration API.
4. Freeze exact session/window/pane/PID before showing the picker; revalidate before delivering input. Add a dedicated literal insertion dispatch using bracketed paste and no Enter; reject control-bearing selection payloads. Bounded, sanitized, read-only previews for files/dirs and Git objects; no external diff/textconv/pager or shell evaluation of targets. Existing Links/Bookmarks open behavior stays available separately.
5. Tests: temporary Git repo including untracked, ignored, renamed/deleted, whitespace/control filenames, branches/tags/stashes/worktrees, detached/unborn/non-Git cases; insertion formatting and exact identity failure. Real disposable popup checks multiselect, provider switches, previews, copy, cancellation, literal paste and existing draft preservation, with clipboard stubs. Run affected packages then required test/build/race/config checks once.
6. Update README, usage, spec and examples as needed. Install the artifact with a reversible backup. Inspect existing keys, then persist an explicit Completion binding after TPM (avoid silently conflicting with the installed plugin), load only those bindings into the live server, and verify launcher/artifact identity. Do not change agent settings or send requests to real agents.

## Astra review

Reviewed by `gpt-6-astra` (high reasoning) before implementation. Accepted:

- Dedicated insertion dispatch, bounded control-free valid UTF-8, unique buffer, bracketed paste and a second exact identity check after creating the buffer. Require bracketed paste mode; never send Enter or editing keys.
- Enter joins raw values with one space and adds no surrounding spaces. Shell quoting is explicit. `@` insertion accepts only unambiguous whitespace/quote-free local paths; it does not promise attachment chips or provider parsing.
- Narrow providers to files, dirs, changed, branches, tags, commits, plus worktrees and existing screen reference extraction. Defer arbitrary screen text, stashes, remotes, reflogs, inferred word replacement, and open actions (already available in Links/Docs).
- Files use NUL-delimited Git results including untracked/nonignored paths; directories use bounded filesystem traversal (including empty dirs), no symlink recursion, documented pruning. Missing paths remain in Changed. Branches omit remote HEAD aliases; commits insert full IDs.
- Freeze origin for the Completion lifetime across provider switches. Also retain foreground command and cwd to reject agent-to-shell/cwd changes. Same-command restart or a changed input dialog cannot be established from pane identity: document that boundary.
- Preview file bytes without executing project tools, reject nonregular files, disable Git external diff/textconv/pagers, make limits/errors visible.
- Real popup acceptance uses a bracketed-paste input fixture with an existing prefix/suffix and event recording, plus disabled paste, stale identity, copy and cancellation checks. Native Codex/Claude interpretation is a separate gate.

The accepted scope supersedes the broader initial proposal above.

## Delivered and verified

- Implemented the accepted eight providers, Alt-9 view navigation, raw/copy/@/shell formats, inert previews and exact-origin bracketed insertion.
- Focused package tests, `make test`, `make race`, `make build`, `go vet ./...`, `make sample-config`, `make validate-config`, and generated/global config validation passed with `GOCACHE=/tmp/tmux-menu-go-build`.
- `scripts/test-completion-picker.py` passed in a disposable real tmux server, including 170-column side preview and 100-column bottom preview. It verified exact draft prefix/suffix, no submit/edit events, multiselect, branch insertion, full commit copy, formatting, refresh/navigation, screen references, moved-origin rejection and disabled-paste copy.
- Installed artifact: `~/.local/bin/tmux-menu`, SHA-256 `d136e43554ac6ef9fb75f63c8200b8add94e07333c2c67edf866cd46f6bad888` (matches `bin/tmux-menu`).
- Prefix+Ctrl-T is saved after TPM in `~/.dotfiles/tmux.conf-mac` and activated live. The prefix is Ctrl-]; Prefix+g still selects the existing move table. No agent settings were edited.
- Original backups: `~/.local/bin/tmux-menu.before-completion-20261117T151553` and `~/.dotfiles/tmux.conf-mac.before-completion-20261117T151553`. Installation receipt: `/tmp/tmux-menu-completion-install.json`.
- Remaining human acceptance: use an unsent draft in the running Codex/Claude client; confirm desired insertion/interpretation of literal @ references. No requests were submitted to real agents during validation.
- Existing dirty work was preserved during implementation. On the later
  commit/push request, typed Links/Bookmarks and Completion were split into
  separate commits; the dotfiles commit is limited to this task's shortcuts.
