package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"tmux-menu/internal/agentstatus"
	"tmux-menu/internal/shellquote"
)

const (
	agentPreviewSnapshotVersion = 1
	agentPreviewMaxChildren     = 24
	agentPreviewCaptureLines    = 300
)

var (
	agentHookNow                      = time.Now
	agentHookExecutable               = os.Executable
	agentHookLookPath                 = exec.LookPath
	agentHookUserHomeDir              = os.UserHomeDir
	agentHookInput          io.Reader = os.Stdin
	agentHookProtocolOutput io.Writer = os.Stdout
	agentHookCommandOutput            = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	agentHookTmuxOutput = func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "tmux", args...).Output()
	}
	agentHookProcessOutput = func(ctx context.Context) ([]byte, error) {
		return exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,state=,command=").Output()
	}
	agentHookCurrentPID               = os.Getpid
	agentHookProviderAncestorResolver = resolveAgentHookProviderAncestor
)

// agentPreviewData is the metadata frozen for one Agents picker generation.
// It deliberately excludes prompts, transcripts, tool input, and model output.
type agentPreviewData struct {
	State           string   `json:"state"`
	Provider        string   `json:"provider"`
	ProviderSession string   `json:"provider_session,omitempty"`
	Source          string   `json:"source"`
	Event           string   `json:"event,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Age             string   `json:"age,omitempty"`
	Fresh           bool     `json:"fresh"`
	Children        []string `json:"children,omitempty"`
}

type agentPreviewSnapshot struct {
	Version int                         `json:"version"`
	Rows    map[string]agentPreviewData `json:"rows"`
}

// runAgentHook is the command handler routed by run for the agent-hook family.
func runAgentHook(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return agentHookUsageError()
	}
	switch args[0] {
	case "ingest", "trace":
		if len(args) != 2 {
			return fmt.Errorf("agent-hook %s requires exactly one provider", args[0])
		}
		provider, err := agentstatus.ParseProvider(args[1])
		if err != nil {
			return err
		}
		// Provider boundary hooks must never block or break the agent. Strict
		// helpers keep diagnostics and tests observable while this path no-ops.
		_ = runAgentHookBoundaryStrict(ctx, args[0], provider, agentHookInput)
		// Current Codex Stop/SubagentStop parsers require JSON on stdout, while
		// an empty object is also a no-op for the other lifecycle events. Keep
		// this protocol response separate from diagnostic/user-visible output.
		if provider == agentstatus.ProviderCodex {
			_, _ = io.WriteString(agentHookProtocolOutput, "{}\n")
		}
		return nil
	case "doctor":
		if len(args) != 1 {
			return fmt.Errorf("agent-hook doctor takes no arguments")
		}
		return runAgentHookDoctor(ctx, os.Stdout)
	case "snapshot":
		if len(args) != 1 {
			return fmt.Errorf("agent-hook snapshot takes no arguments")
		}
		return writeAgentStatusSnapshotCommand(ctx, os.Stdout)
	case "snippets":
		return runAgentHookSnippets(args[1:], os.Stdout)
	case "preview":
		if len(args) != 3 {
			return fmt.Errorf("agent-hook preview requires snapshot path and pane ID")
		}
		return runAgentPreview(ctx, args[1], args[2], os.Stdout)
	default:
		return fmt.Errorf("unknown agent-hook command %q: %w", args[0], agentHookUsageError())
	}
}

func agentHookUsageError() error {
	return errors.New("usage: tmux-menu agent-hook <ingest|trace> <codex|claude> | doctor | snapshot | snippets [codex|claude] [ingest|trace]")
}

func runAgentHookBoundaryStrict(ctx context.Context, mode string, provider agentstatus.Provider, input io.Reader) error {
	if mode != "ingest" && mode != "trace" {
		return fmt.Errorf("unsupported hook mode %q", mode)
	}
	claim, owned, err := resolveAgentHookPane(ctx)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	providerPID, err := agentHookProviderAncestorResolver(ctx, provider, agentHookCurrentPID(), claim.PanePID)
	if err != nil {
		return err
	}
	claim.ProviderPID = providerPID
	event, trace, decodeErr := agentstatus.DecodeHook(provider, claim, input, agentHookNow())
	if mode == "ingest" && decodeErr != nil {
		return decodeErr
	}
	store, storeErr := openDefaultAgentStatusStore()
	if storeErr != nil {
		return storeErr
	}
	if mode == "trace" {
		if err := store.AppendTrace(ctx, trace); err != nil {
			return err
		}
		return decodeErr
	}
	if event.Kind == agentstatus.EventObservedOnly {
		return nil
	}
	_, _, err = store.Apply(ctx, event)
	return err
}

func openDefaultAgentStatusStore() (*agentstatus.Store, error) {
	root, err := agentstatus.DefaultStateDir()
	if err != nil {
		return nil, err
	}
	return agentstatus.NewStore(root, agentstatus.DefaultPolicy())
}

func resolveAgentHookPane(ctx context.Context) (agentstatus.PaneIdentity, bool, error) {
	tmuxEnv := strings.TrimSpace(os.Getenv("TMUX"))
	paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if tmuxEnv == "" || paneID == "" {
		return agentstatus.PaneIdentity{}, false, nil
	}
	if !validStableTmuxID(paneID, '%') {
		return agentstatus.PaneIdentity{}, false, fmt.Errorf("invalid TMUX_PANE")
	}
	const fieldSeparator = "\x1f"
	format := strings.Join([]string{"#{pane_id}", "#{pane_pid}", "#{session_id}"}, fieldSeparator)
	lookupCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	out, err := agentHookTmuxOutput(lookupCtx, "display-message", "-p", "-t", paneID, format)
	if err != nil {
		return agentstatus.PaneIdentity{}, false, fmt.Errorf("resolve tmux pane ownership: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), fieldSeparator)
	if len(parts) != 3 || parts[0] != paneID || !validStableTmuxID(parts[0], '%') || !validStableTmuxID(parts[2], '$') {
		return agentstatus.PaneIdentity{}, false, fmt.Errorf("tmux pane ownership mismatch")
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil || pid <= 0 {
		return agentstatus.PaneIdentity{}, false, fmt.Errorf("invalid tmux pane PID")
	}
	return agentstatus.PaneIdentity{
		ServerID:      agentstatus.ServerFingerprint(tmuxEnv),
		PaneID:        parts[0],
		PanePID:       pid,
		TmuxSessionID: parts[2],
	}, true, nil
}

func validStableTmuxID(value string, prefix byte) bool {
	if len(value) < 2 || value[0] != prefix {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func resolveAgentHookProviderAncestor(ctx context.Context, provider agentstatus.Provider, hookPID, panePID int) (int, error) {
	if hookPID <= 0 || panePID <= 0 {
		return 0, fmt.Errorf("invalid hook process identity")
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	out, err := agentHookProcessOutput(lookupCtx)
	if err != nil {
		return 0, fmt.Errorf("read hook process ancestry: %w", err)
	}
	pid, ok := findAgentHookProviderAncestor(provider, hookPID, panePID, parseProcessList(string(out)))
	if !ok {
		return 0, fmt.Errorf("no matching %s process ancestor in tmux pane", provider)
	}
	return pid, nil
}

func findAgentHookProviderAncestor(provider agentstatus.Provider, hookPID, panePID int, processes []processInfo) (int, bool) {
	byPID := make(map[int]processInfo, len(processes))
	for _, process := range processes {
		if process.pid > 0 {
			byPID[process.pid] = process
		}
	}
	current := hookPID
	seen := map[int]bool{hookPID: true}
	providerPID := 0
	for depth := 0; depth < 64; depth++ {
		process, ok := byPID[current]
		if !ok || process.ppid <= 0 || seen[process.ppid] {
			return 0, false
		}
		ancestor, ok := byPID[process.ppid]
		if !ok {
			return 0, false
		}
		seen[ancestor.pid] = true
		if processAgentName(ancestor.command) == string(provider) {
			// Keep walking to the pane root and retain the outermost matching
			// provider process. The picker sees the stable CLI wrapper before
			// its native child, so hook identity must use that same incarnation.
			providerPID = ancestor.pid
		}
		if ancestor.pid == panePID {
			return providerPID, providerPID > 0
		}
		current = ancestor.pid
	}
	return 0, false
}

func runAgentHookSnippets(args []string, out io.Writer) error {
	provider := ""
	mode := "ingest"
	if len(args) > 2 {
		return agentHookUsageError()
	}
	if len(args) >= 1 {
		first := strings.ToLower(args[0])
		if first == "ingest" || first == "trace" {
			mode = first
			if len(args) == 2 {
				return agentHookUsageError()
			}
		} else {
			provider = first
			if _, err := agentstatus.ParseProvider(provider); err != nil {
				return err
			}
		}
	}
	if len(args) == 2 {
		mode = strings.ToLower(args[1])
	}
	if mode != "ingest" && mode != "trace" {
		return fmt.Errorf("snippet mode must be ingest or trace")
	}
	exe, err := agentHookCommandPath()
	if err != nil {
		return err
	}
	providers := []string{provider}
	if provider == "" {
		providers = []string{"codex", "claude"}
	}
	for i, current := range providers {
		if i > 0 {
			fmt.Fprintln(out)
		}
		command := strings.Join([]string{shellquote.Quote(exe), "agent-hook", mode, current}, " ")
		fmt.Fprintf(out, "# %s hook snippet (%s)\n", current, mode)
		fmt.Fprintln(out, "# Review in the provider's /hooks inspector before relying on it. This command is synchronous, user-silent, and fail-open.")
		var snippet string
		if current == "codex" {
			snippet, err = codexHookSnippet(command)
		} else {
			snippet, err = claudeHookSnippet(command)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, snippet)
	}
	return nil
}

func agentHookCommandPath() (string, error) {
	exe, err := agentHookExecutable()
	if err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	return exe, nil
}

func codexHookSnippet(command string) (string, error) {
	return marshalHookSnippet(providerHookEvents("codex"), command, true)
}

func claudeHookSnippet(command string) (string, error) {
	return marshalHookSnippet(providerHookEvents("claude"), command, false)
}

func providerHookEvents(provider string) []string {
	if provider == "codex" {
		return []string{
			"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest",
			"Stop", "SessionEnd", "SubagentStart", "SubagentStop",
		}
	}
	return []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PostToolBatch",
		"PermissionRequest", "PermissionDenied", "Notification", "Stop", "StopFailure", "SessionEnd",
		"SubagentStart", "SubagentStop",
	}
}

func marshalHookSnippet(events []string, command string, codex bool) (string, error) {
	hooks := make(map[string][]map[string]any, len(events))
	timeout := 5
	if codex {
		// Codex SessionEnd accepts at most three seconds. One provider-wide
		// value keeps the generated snippet reviewable and valid for every edge.
		timeout = 3
	}
	for _, event := range events {
		group := map[string]any{
			"hooks": []map[string]any{{
				"type":    "command",
				"command": command,
				"timeout": timeout,
			}},
		}
		if !codex && event == "Notification" {
			group["matcher"] = "permission_prompt"
		}
		hooks[event] = []map[string]any{group}
	}
	root := map[string]any{"hooks": hooks}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	file := "~/.claude/settings.json"
	if codex {
		file = "~/.codex/hooks.json"
	}
	return fmt.Sprintf("# Merge into %s; do not replace unrelated hooks.\n%s", file, b), nil
}

func runAgentHookDoctor(ctx context.Context, out io.Writer) error {
	var failures []string
	check := func(ok bool, success, failure string) {
		if ok {
			fmt.Fprintf(out, "ok      %s\n", success)
			return
		}
		fmt.Fprintf(out, "problem %s\n", failure)
		failures = append(failures, failure)
	}

	if exe, err := agentHookCommandPath(); err == nil {
		info, statErr := os.Stat(exe)
		check(statErr == nil && !info.IsDir(), "tmux-menu binary is available", "tmux-menu executable is unavailable")
	} else {
		check(false, "", "tmux-menu executable is unavailable")
	}
	_, tmuxErr := agentHookLookPath("tmux")
	check(tmuxErr == nil, "tmux is installed", "tmux is not on PATH")
	executable, _ := agentHookCommandPath()
	for _, provider := range []string{"codex", "claude"} {
		path, err := agentHookLookPath(provider)
		check(err == nil, provider+" provider is installed", provider+" provider is not on PATH")
		if err == nil {
			versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			version, versionErr := agentHookCommandOutput(versionCtx, path, "--version")
			cancel()
			if versionErr != nil {
				check(false, "", provider+" version could not be read")
			} else {
				fmt.Fprintf(out, "info    %s version: %s\n", provider, sanitizeDiagnosticLine(string(version)))
			}
		}
		inspection, configErr := inspectProviderHookConfigDetails(provider, executable)
		if errors.Is(configErr, os.ErrNotExist) {
			check(false, "", provider+" tmux-menu hook is not configured")
		} else if configErr != nil {
			check(false, "", provider+" hook configuration is unreadable or invalid")
		} else {
			check(inspection.Configured, provider+" tmux-menu hook is configured", provider+" tmux-menu hook is not configured")
			if inspection.Configured {
				check(len(inspection.MissingEvents) == 0, provider+" hook covers the required event set", provider+" hook is missing required events")
				if len(inspection.MissingEvents) == 0 {
					check(inspection.Current, provider+" hook uses the current executable", provider+" hook does not consistently use the current executable and mode")
				}
			}
		}
	}
	claim, owned, err := resolveAgentHookPane(ctx)
	check(err == nil, "tmux ownership lookup succeeded", "tmux ownership lookup failed")
	if err == nil {
		if owned {
			check(claim.ServerID != "" && claim.PanePID > 0, "hook is bound to a live tmux pane", "tmux ownership claim is incomplete")
		} else {
			fmt.Fprintln(out, "info    outside tmux; ingest and trace safely no-op")
		}
	}
	root, rootErr := agentstatus.DefaultStateDir()
	check(rootErr == nil, "state path resolved", "state path could not be resolved")
	if rootErr == nil {
		if info, statErr := os.Lstat(root); errors.Is(statErr, os.ErrNotExist) {
			fmt.Fprintln(out, "info    state directory does not exist yet; the first ingest/trace event will create it privately")
			printTraceMatrix(out, nil)
		} else if statErr != nil {
			check(false, "", "state directory metadata is unreadable")
		} else {
			private := info.IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0o077 == 0
			check(private, "state directory is private", "state directory must be a private real directory")
			if private {
				store, storeErr := agentstatus.NewStore(root, agentstatus.DefaultPolicy())
				check(storeErr == nil, "state store schema and permissions are usable", "state store schema or permissions are invalid")
				if storeErr == nil {
					validateErr := store.ValidateReadOnly(ctx)
					check(validateErr == nil, "state record schema is compatible", "state record schema is incompatible")
					traces, traceErr := store.RecentTraces(ctx, 512)
					check(traceErr == nil, "trace log is readable", "trace log is unreadable")
					if traceErr == nil {
						printTraceMatrix(out, traces)
					}
				}
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("agent-hook doctor found %d problem(s)", len(failures))
	}
	return nil
}

func traceCoverage(traces []agentstatus.TraceEntry) (accepted, rejected int) {
	for _, trace := range traces {
		// TraceEntry intentionally owns only metadata. Avoid printing or
		// reflecting field values here; classify its exported acceptance bit.
		if trace.Accepted {
			accepted++
		} else {
			rejected++
		}
	}
	return accepted, rejected
}

func sanitizeDiagnosticLine(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\n", " "), "\r", " "))
	return safePreviewText(value, 120)
}

type providerHookInspection struct {
	Configured    bool
	Current       bool
	AnyCurrent    bool
	MissingEvents []string
}

type providerHookConfigDocument struct {
	Hooks map[string][]providerHookGroup `json:"hooks" toml:"hooks"`
}

type providerHookGroup struct {
	Hooks []providerHookCommand `json:"hooks" toml:"hooks"`
}

type providerHookCommand struct {
	Command string `json:"command" toml:"command"`
}

func inspectProviderHookConfigDetails(provider, executable string) (providerHookInspection, error) {
	home, err := agentHookUserHomeDir()
	if err != nil {
		return providerHookInspection{}, err
	}
	type configSource struct {
		path   string
		format string
	}
	sources := []configSource{{path: filepath.Join(home, ".claude", "settings.json"), format: "json"}}
	if provider == "codex" {
		// Codex merges the standalone hooks file with inline config. Always
		// inspect both active sources; an existing hooks.json must not mask
		// additional hooks in config.toml.
		sources = []configSource{
			{path: filepath.Join(home, ".codex", "hooks.json"), format: "json"},
			{path: filepath.Join(home, ".codex", "config.toml"), format: "toml"},
		}
	}

	eventCommands := make(map[string][]string)
	foundSource := false
	for _, source := range sources {
		contents, readErr := readBoundedHookConfig(source.path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return providerHookInspection{}, readErr
		}
		foundSource = true
		document, decodeErr := decodeHookConfig(contents, source.format)
		if decodeErr != nil {
			return providerHookInspection{}, decodeErr
		}
		for event, commands := range hookEventCommands(document) {
			event = normalizeHookEventName(event)
			eventCommands[event] = append(eventCommands[event], commands...)
		}
	}
	if !foundSource {
		return providerHookInspection{}, os.ErrNotExist
	}

	inspection := providerHookInspection{}
	coverage := make(map[string]bool)
	currentCoverage := map[string]map[string]bool{
		"ingest": {},
		"trace":  {},
	}
	for event, commands := range eventCommands {
		for _, command := range commands {
			mode, ok := hookProviderCommandMode(command, provider)
			if !ok {
				continue
			}
			inspection.Configured = true
			coverage[event] = true
			if hookCommandUsesExecutable(command, executable, mode, provider) {
				inspection.AnyCurrent = true
				currentCoverage[mode][event] = true
			}
		}
	}
	for _, event := range providerHookEvents(provider) {
		key := normalizeHookEventName(event)
		if !coverage[key] {
			inspection.MissingEvents = append(inspection.MissingEvents, event)
		}
	}
	if len(inspection.MissingEvents) == 0 {
		for _, mode := range []string{"ingest", "trace"} {
			complete := true
			for _, event := range providerHookEvents(provider) {
				if !currentCoverage[mode][normalizeHookEventName(event)] {
					complete = false
					break
				}
			}
			if complete {
				inspection.Current = true
				break
			}
		}
	}
	return inspection, nil
}

func readBoundedHookConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, statErr := f.Stat(); statErr != nil || info.IsDir() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("hook config is unreadable or too large")
	}
	contents, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > 1<<20 {
		return nil, fmt.Errorf("hook config is too large")
	}
	return contents, nil
}

func decodeHookConfig(contents []byte, format string) (providerHookConfigDocument, error) {
	if format == "toml" {
		var raw struct {
			Hooks map[string]any `toml:"hooks"`
		}
		if err := toml.Unmarshal(contents, &raw); err != nil {
			return providerHookConfigDocument{}, err
		}
		document := providerHookConfigDocument{Hooks: make(map[string][]providerHookGroup)}
		for event, value := range raw.Hooks {
			// Codex owns [hooks.state] for trust metadata. It is not a hook
			// event and must not make the read-only doctor reject config.toml.
			if _, ok := value.([]any); !ok {
				continue
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return providerHookConfigDocument{}, err
			}
			var groups []providerHookGroup
			if err := json.Unmarshal(encoded, &groups); err != nil {
				return providerHookConfigDocument{}, err
			}
			document.Hooks[event] = groups
		}
		return document, nil
	}
	var document providerHookConfigDocument
	dec := json.NewDecoder(bytes.NewReader(contents))
	if err := dec.Decode(&document); err != nil {
		return providerHookConfigDocument{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return providerHookConfigDocument{}, fmt.Errorf("hook config has trailing data")
	}
	return document, nil
}

func hookEventCommands(document providerHookConfigDocument) map[string][]string {
	events := make(map[string][]string, len(document.Hooks))
	for event, groups := range document.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if hook.Command != "" {
					events[event] = append(events[event], hook.Command)
				}
			}
		}
	}
	return events
}

func normalizeHookEventName(event string) string {
	return strings.ToLower(strings.TrimSpace(event))
}

func hookProviderCommandMode(command, provider string) (string, bool) {
	command = strings.TrimSpace(command)
	for _, mode := range []string{"ingest", "trace"} {
		if strings.HasSuffix(command, " agent-hook "+mode+" "+provider) {
			return mode, true
		}
	}
	return "", false
}

func hookCommandUsesExecutable(command, executable, mode, provider string) bool {
	if executable == "" {
		return false
	}
	command = strings.TrimSpace(command)
	suffix := " agent-hook " + mode + " " + provider
	if command == shellquote.Quote(executable)+suffix {
		return true
	}
	return isPlainHookExecutable(executable) && command == executable+suffix
}

func isPlainHookExecutable(executable string) bool {
	if executable == "" {
		return false
	}
	for _, r := range executable {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("/._-+", r) {
			continue
		}
		return false
	}
	return true
}

func printTraceMatrix(out io.Writer, traces []agentstatus.TraceEntry) {
	accepted, rejected := traceCoverage(traces)
	fmt.Fprintf(out, "info    recent metadata traces: %d accepted, %d rejected\n", accepted, rejected)
	for _, provider := range []agentstatus.Provider{agentstatus.ProviderCodex, agentstatus.ProviderClaude} {
		observed := traceEdgesForProvider(traces, provider)
		for _, edge := range providerTraceEdges(provider) {
			status := "missing"
			detail := "no recent trace evidence"
			if observed[edge] {
				status, detail = "observed", "recent metadata edge observed"
			}
			fmt.Fprintf(out, "%-8s %-34s %s\n", status, string(provider)+"/"+edge, detail)
		}
		for _, scenario := range []struct {
			name   string
			detail string
		}{
			{"parallel-tool", "unverified; individual tool edges do not prove concurrency"},
			{"approval-path", "unverified; metadata does not distinguish auto-approved from real approval"},
			{"interrupt-resume", "unverified; requires a controlled provider test"},
			{"hard-kill", "unverified; process termination may emit no hook"},
		} {
			fmt.Fprintf(out, "%-8s %-34s %s\n", "manual", string(provider)+"/"+scenario.name, scenario.detail)
		}
	}
	if len(traces) == 0 {
		fmt.Fprintln(out, "info    no trace coverage yet; run generated trace snippets before enabling ingest")
	}
}

func providerTraceEdges(provider agentstatus.Provider) []string {
	if provider == agentstatus.ProviderClaude {
		return []string{
			"session-start", "session-end", "prompt", "pre-tool", "post-tool",
			"tool-batch", "permission-request", "permission-denial", "stop", "child-start", "child-stop",
		}
	}
	return []string{
		"session-start", "session-end", "prompt", "pre-tool", "post-tool",
		"permission-request", "stop", "child-start", "child-stop",
	}
}

func traceEdgesForProvider(traces []agentstatus.TraceEntry, provider agentstatus.Provider) map[string]bool {
	observed := make(map[string]bool)
	edgeByRawEvent := map[string]string{
		"sessionstart":      "session-start",
		"sessionend":        "session-end",
		"userpromptsubmit":  "prompt",
		"pretooluse":        "pre-tool",
		"posttooluse":       "post-tool",
		"posttoolbatch":     "tool-batch",
		"permissionrequest": "permission-request",
		"permissiondenied":  "permission-denial",
		"stop":              "stop",
		"subagentstart":     "child-start",
		"subagentstop":      "child-stop",
	}
	for _, trace := range traces {
		if trace.Provider != provider || !trace.Accepted {
			continue
		}
		rawEvent := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(trace.RawEvent)))
		if edge := edgeByRawEvent[rawEvent]; edge != "" {
			observed[edge] = true
		}
	}
	return observed
}

func writeAgentPreviewSnapshot(rows map[string]agentPreviewData) (string, func(), error) {
	if rows == nil {
		rows = map[string]agentPreviewData{}
	}
	clean := make(map[string]agentPreviewData, len(rows))
	for paneID, row := range rows {
		if !validStableTmuxID(paneID, '%') {
			return "", nil, fmt.Errorf("invalid preview pane ID")
		}
		row.State = safePreviewText(row.State, 32)
		row.Provider = safePreviewText(row.Provider, 32)
		row.ProviderSession = safePreviewText(row.ProviderSession, 128)
		row.Source = safePreviewText(row.Source, 48)
		row.Event = safePreviewText(row.Event, 64)
		row.Reason = safePreviewText(row.Reason, 160)
		row.Age = safePreviewText(row.Age, 32)
		if len(row.Children) > agentPreviewMaxChildren {
			row.Children = append([]string(nil), row.Children[:agentPreviewMaxChildren]...)
		} else {
			row.Children = append([]string(nil), row.Children...)
		}
		for i := range row.Children {
			row.Children[i] = safePreviewText(row.Children[i], 160)
		}
		clean[paneID] = row
	}
	dir, err := os.MkdirTemp("", "tmux-menu-agent-preview-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "snapshot.json")
	if err := writeAgentPreviewSnapshotFile(path, agentPreviewSnapshot{Version: agentPreviewSnapshotVersion, Rows: clean}); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func writeAgentPreviewSnapshotFile(path string, snapshot agentPreviewSnapshot) error {
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func agentPreviewCommandForSnapshot(executable, snapshotPath string) string {
	command := strings.Join([]string{
		shellquote.Quote(executable), "agent-hook", "preview", shellquote.Quote(snapshotPath), "{}",
	}, " ")
	return "test -n {} && " + command
}

func runAgentPreview(ctx context.Context, snapshotPath, paneID string, out io.Writer) error {
	if !validStableTmuxID(paneID, '%') {
		return fmt.Errorf("invalid preview pane ID")
	}
	snapshot, err := readAgentPreviewSnapshot(snapshotPath)
	if err != nil {
		return err
	}
	row, ok := snapshot.Rows[paneID]
	if !ok {
		return fmt.Errorf("pane is not present in frozen preview")
	}
	printAgentPreviewHeader(out, row)
	captured, err := agentHookTmuxOutput(ctx, "capture-pane", "-e", "-p", "-S", "-"+strconv.Itoa(agentPreviewCaptureLines), "-t", paneID)
	if err != nil {
		fmt.Fprintln(out, "\n[scrollback unavailable]")
		return nil
	}
	trimmed := trimTrailingBlankLines(string(captured))
	if trimmed != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, trimmed)
	}
	return nil
}

func readAgentPreviewSnapshot(path string) (agentPreviewSnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return agentPreviewSnapshot{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 1<<20 {
		return agentPreviewSnapshot{}, fmt.Errorf("unsafe preview snapshot")
	}
	f, err := os.Open(path)
	if err != nil {
		return agentPreviewSnapshot{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	var snapshot agentPreviewSnapshot
	if err := dec.Decode(&snapshot); err != nil {
		return agentPreviewSnapshot{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return agentPreviewSnapshot{}, fmt.Errorf("preview snapshot has trailing data")
	}
	if snapshot.Version != agentPreviewSnapshotVersion || snapshot.Rows == nil {
		return agentPreviewSnapshot{}, fmt.Errorf("unsupported preview snapshot schema")
	}
	return snapshot, nil
}

func printAgentPreviewHeader(out io.Writer, row agentPreviewData) {
	freshness := "stale"
	if row.Fresh {
		freshness = "fresh"
	}
	parts := []string{safePreviewText(nonEmpty(row.Provider, "agent"), 32), safePreviewText(nonEmpty(row.State, "unknown"), 32), freshness}
	if row.Age != "" {
		parts = append(parts, safePreviewText(row.Age, 32))
	}
	fmt.Fprintf(out, "Status: %s\n", strings.Join(parts, " | "))
	if row.Source != "" || row.Event != "" {
		fmt.Fprintf(out, "Evidence: %s", safePreviewText(nonEmpty(row.Source, "unknown"), 48))
		if row.Event != "" {
			fmt.Fprintf(out, " (%s)", safePreviewText(row.Event, 64))
		}
		fmt.Fprintln(out)
	}
	if session := boundedOpaqueID(row.ProviderSession); session != "" {
		fmt.Fprintf(out, "Session: %s\n", session)
	}
	if row.Reason != "" {
		fmt.Fprintf(out, "Reason: %s\n", safePreviewText(row.Reason, 160))
	}
	children := append([]string(nil), row.Children...)
	sort.Strings(children)
	for _, child := range children {
		fmt.Fprintf(out, "Child: %s\n", safePreviewText(child, 160))
	}
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func boundedOpaqueID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() >= 16 {
			break
		}
	}
	return b.String()
}

func safePreviewText(value string, max int) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= max {
			break
		}
	}
	return b.String()
}

func trimTrailingBlankLines(value string) string {
	scanner := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(value, "\r\n", "\n")))
	lines := make([]string, 0)
	lastNonBlank := -1
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if strings.TrimSpace(scanner.Text()) != "" {
			lastNonBlank = len(lines) - 1
		}
	}
	if lastNonBlank < 0 {
		return ""
	}
	return strings.Join(lines[:lastNonBlank+1], "\n")
}
