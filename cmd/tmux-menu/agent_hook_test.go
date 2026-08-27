package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-menu/internal/agentstatus"
)

func TestAgentHookBoundaryOutsideTmuxIsFailOpen(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	if err := runAgentHookBoundaryStrict(context.Background(), "ingest", agentstatus.ProviderCodex, strings.NewReader("not json")); err != nil {
		t.Fatalf("outside-tmux boundary error = %v, want nil", err)
	}
	if err := runAgentHook(context.Background(), []string{"ingest", "codex"}); err != nil {
		t.Fatalf("fail-open command error = %v, want nil", err)
	}
}

func TestAgentHookBoundaryUsageErrorsRemainVisible(t *testing.T) {
	for _, args := range [][]string{{"ingest"}, {"ingest", "other"}, {"trace", "codex", "extra"}, {"unknown"}} {
		if err := runAgentHook(context.Background(), args); err == nil {
			t.Fatalf("runAgentHook(%q) error = nil, want usage/provider error", args)
		}
	}
}

func TestHookClaimUsesTargetedTmuxIdentity(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,123,0")
	t.Setenv("TMUX_PANE", "%7")
	original := agentHookTmuxOutput
	t.Cleanup(func() { agentHookTmuxOutput = original })
	agentHookTmuxOutput = func(_ context.Context, args ...string) ([]byte, error) {
		want := []string{"display-message", "-p", "-t", "%7", "#{pane_id}\x1f#{pane_pid}\x1f#{session_id}"}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("tmux args = %q, want %q", args, want)
		}
		return []byte("%7\x1f987\x1f$2\n"), nil
	}
	claim, owned, err := resolveAgentHookPane(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !owned || claim.PaneID != "%7" || claim.PanePID != 987 || claim.TmuxSessionID != "$2" || claim.ServerID == "" {
		t.Fatalf("claim = %+v, owned = %v", claim, owned)
	}
}

func TestHookClaimRejectsMismatch(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,123,0")
	t.Setenv("TMUX_PANE", "%7")
	original := agentHookTmuxOutput
	t.Cleanup(func() { agentHookTmuxOutput = original })
	agentHookTmuxOutput = func(context.Context, ...string) ([]byte, error) {
		return []byte("%8\x1f987\x1f$2\n"), nil
	}
	if _, _, err := resolveAgentHookPane(context.Background()); err == nil {
		t.Fatal("ownership mismatch error = nil")
	}
	// The provider-facing command swallows the same strict failure.
	if err := runAgentHook(context.Background(), []string{"ingest", "claude"}); err != nil {
		t.Fatalf("fail-open command error = %v", err)
	}
}

func TestAgentHookCommandSwallowsStrictDecodeFailure(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	t.Setenv("TMUX_MENU_AGENT_STATE_DIR", stateRoot)
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,123,0")
	t.Setenv("TMUX_PANE", "%7")
	originalOutput := agentHookTmuxOutput
	originalInput := agentHookInput
	t.Cleanup(func() {
		agentHookTmuxOutput = originalOutput
		agentHookInput = originalInput
	})
	agentHookTmuxOutput = func(context.Context, ...string) ([]byte, error) {
		return []byte("%7\x1f987\x1f$2\n"), nil
	}
	agentHookInput = strings.NewReader("not-json")
	if err := runAgentHookBoundaryStrict(context.Background(), "ingest", agentstatus.ProviderCodex, strings.NewReader("not-json")); err == nil {
		t.Fatal("strict decoder error = nil")
	}
	if err := runAgentHook(context.Background(), []string{"ingest", "codex"}); err != nil {
		t.Fatalf("provider boundary leaked strict failure: %v", err)
	}
	if _, err := os.Stat(stateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid ingest created state root: %v", err)
	}
}

func TestAgentHookProviderProtocolOutputOnSwallowedStrictFailure(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,123,0")
	t.Setenv("TMUX_PANE", "%7")
	originalTmuxOutput := agentHookTmuxOutput
	originalInput := agentHookInput
	originalProtocolOutput := agentHookProtocolOutput
	t.Cleanup(func() {
		agentHookTmuxOutput = originalTmuxOutput
		agentHookInput = originalInput
		agentHookProtocolOutput = originalProtocolOutput
	})
	agentHookTmuxOutput = func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("strict ownership lookup failed")
	}

	for _, mode := range []string{"ingest", "trace"} {
		for _, provider := range []string{"codex", "claude"} {
			t.Run(mode+"/"+provider, func(t *testing.T) {
				agentHookInput = strings.NewReader("not-json")
				var protocol bytes.Buffer
				agentHookProtocolOutput = &protocol
				if err := runAgentHook(context.Background(), []string{mode, provider}); err != nil {
					t.Fatalf("valid provider command leaked strict failure: %v", err)
				}
				want := ""
				if provider == "codex" {
					want = "{}\n"
				}
				if got := protocol.String(); got != want {
					t.Fatalf("protocol output = %q, want exactly %q", got, want)
				}
			})
		}
	}
}

func TestAgentHookTracePersistsMetadataWithoutPrompt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	t.Setenv("TMUX_MENU_AGENT_STATE_DIR", root)
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,123,0")
	t.Setenv("TMUX_PANE", "%7")
	originalOutput := agentHookTmuxOutput
	originalResolver := agentHookProviderAncestorResolver
	t.Cleanup(func() {
		agentHookTmuxOutput = originalOutput
		agentHookProviderAncestorResolver = originalResolver
	})
	agentHookTmuxOutput = func(context.Context, ...string) ([]byte, error) {
		return []byte("%7\x1f987\x1f$2\n"), nil
	}
	agentHookProviderAncestorResolver = func(context.Context, agentstatus.Provider, int, int) (int, error) {
		return 876, nil
	}
	payload := `{"hook_event_name":"UserPromptSubmit","session_id":"session-1","prompt_id":"prompt-1","prompt":"SUPER_PRIVATE_PROMPT"}`
	if err := runAgentHookBoundaryStrict(context.Background(), "trace", agentstatus.ProviderClaude, strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	store, err := agentstatus.NewStore(root, agentstatus.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	traces, err := store.RecentTraces(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].Pane.ProviderPID != 876 {
		t.Fatalf("trace provider incarnation = %+v, want PID 876", traces)
	}
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(contents, []byte("SUPER_PRIVATE_PROMPT")) {
			t.Fatalf("trace file %s retained raw prompt", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFindAgentHookProviderAncestorBindsMatchingIncarnation(t *testing.T) {
	processes := []processInfo{
		{pid: 100, ppid: 1, command: "zsh"},
		{pid: 300, ppid: 100, command: "node /opt/bin/codex resume session"},
		{pid: 350, ppid: 300, command: "/opt/lib/codex/vendor/codex resume session"},
		{pid: 400, ppid: 350, command: "sh -c '/tmp/tmux-menu agent-hook ingest codex'"},
		{pid: 500, ppid: 400, command: "/tmp/tmux-menu agent-hook ingest codex"},
	}
	if got, ok := findAgentHookProviderAncestor(agentstatus.ProviderCodex, 500, 100, processes); !ok || got != 300 {
		t.Fatalf("ancestor = %d, %v; want provider PID 300", got, ok)
	}
	if got, ok := findAgentHookProviderAncestor(agentstatus.ProviderClaude, 500, 100, processes); ok || got != 0 {
		t.Fatalf("mismatched provider ancestor = %d, %v; want none", got, ok)
	}
}

func TestFindAgentHookProviderAncestorIsPaneBoundAndCycleSafe(t *testing.T) {
	for _, tt := range []struct {
		name      string
		processes []processInfo
	}{
		{
			name: "provider above pane",
			processes: []processInfo{
				{pid: 50, ppid: 1, command: "codex"},
				{pid: 100, ppid: 50, command: "zsh"},
				{pid: 500, ppid: 100, command: "tmux-menu"},
			},
		},
		{
			name: "cycle",
			processes: []processInfo{
				{pid: 100, ppid: 500, command: "zsh"},
				{pid: 500, ppid: 100, command: "tmux-menu"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := findAgentHookProviderAncestor(agentstatus.ProviderCodex, 500, 100, tt.processes); ok || got != 0 {
				t.Fatalf("ancestor = %d, %v; want none", got, ok)
			}
		})
	}
}

func TestAgentHookBoundaryRejectsMissingProviderAncestorWithoutState(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	t.Setenv("TMUX_MENU_AGENT_STATE_DIR", stateRoot)
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,123,0")
	t.Setenv("TMUX_PANE", "%7")
	originalTmuxOutput := agentHookTmuxOutput
	originalProcessOutput := agentHookProcessOutput
	originalCurrentPID := agentHookCurrentPID
	originalResolver := agentHookProviderAncestorResolver
	t.Cleanup(func() {
		agentHookTmuxOutput = originalTmuxOutput
		agentHookProcessOutput = originalProcessOutput
		agentHookCurrentPID = originalCurrentPID
		agentHookProviderAncestorResolver = originalResolver
	})
	agentHookTmuxOutput = func(context.Context, ...string) ([]byte, error) {
		return []byte("%7\x1f100\x1f$2\n"), nil
	}
	agentHookProcessOutput = func(context.Context) ([]byte, error) {
		return []byte("100 1 S zsh\n400 100 S sh -c hook\n500 400 R tmux-menu agent-hook ingest codex\n"), nil
	}
	agentHookCurrentPID = func() int { return 500 }
	agentHookProviderAncestorResolver = resolveAgentHookProviderAncestor
	payload := `{"hook_event_name":"SessionStart","session_id":"session-1"}`
	if err := runAgentHookBoundaryStrict(context.Background(), "ingest", agentstatus.ProviderCodex, strings.NewReader(payload)); err == nil {
		t.Fatal("strict boundary accepted a hook without a Codex ancestor")
	}
	if err := runAgentHook(context.Background(), []string{"ingest", "codex"}); err != nil {
		t.Fatalf("provider boundary did not remain fail-open: %v", err)
	}
	if _, err := os.Stat(stateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unowned hook created state: %v", err)
	}
}

func TestHookSnippetsUseNestedProviderShapeAndExecutable(t *testing.T) {
	original := agentHookExecutable
	t.Cleanup(func() { agentHookExecutable = original })
	agentHookExecutable = func() (string, error) { return "/Applications/Tmux Menu/bin/tmux-menu", nil }
	var out bytes.Buffer
	if err := runAgentHookSnippets([]string{"codex", "trace"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`"hooks": {`, `"PermissionRequest": [`, `"type": "command"`,
		`'/Applications/Tmux Menu/bin/tmux-menu' agent-hook trace codex`,
		`Merge into ~/.codex/hooks.json`, `Review in the provider's /hooks inspector`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("snippet missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"async"`) {
		t.Fatalf("causal hook snippet must remain synchronous:\n%s", got)
	}
}

func TestHookSnippetsCanGenerateBothProvidersInTraceMode(t *testing.T) {
	original := agentHookExecutable
	t.Cleanup(func() { agentHookExecutable = original })
	agentHookExecutable = func() (string, error) { return "/usr/local/bin/tmux-menu", nil }
	var out bytes.Buffer
	if err := runAgentHookSnippets([]string{"trace"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"agent-hook trace codex", "agent-hook trace claude"} {
		if !strings.Contains(got, want) {
			t.Errorf("combined snippet missing %q", want)
		}
	}
	for _, unsupported := range []string{"codex/tool-batch", "codex/permission-denial"} {
		if strings.Contains(got, unsupported) {
			t.Errorf("trace matrix reported unsupported edge %q:\n%s", unsupported, got)
		}
	}
}

func TestHookSnippetTimeoutsRespectProviderLimits(t *testing.T) {
	for _, tt := range []struct {
		name     string
		snippet  func(string) (string, error)
		timeout  float64
		eventKey string
	}{
		{name: "codex", snippet: codexHookSnippet, timeout: 3, eventKey: "SessionEnd"},
		{name: "claude", snippet: claudeHookSnippet, timeout: 5, eventKey: "SessionEnd"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rendered, err := tt.snippet("tmux-menu agent-hook trace " + tt.name)
			if err != nil {
				t.Fatal(err)
			}
			jsonStart := strings.Index(rendered, "{")
			if jsonStart < 0 {
				t.Fatalf("snippet has no JSON: %s", rendered)
			}
			var document struct {
				Hooks map[string][]struct {
					Hooks []struct {
						Timeout float64 `json:"timeout"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(rendered[jsonStart:]), &document); err != nil {
				t.Fatal(err)
			}
			for event, groups := range document.Hooks {
				if len(groups) != 1 || len(groups[0].Hooks) != 1 {
					t.Fatalf("%s shape = %+v", event, groups)
				}
				if got := groups[0].Hooks[0].Timeout; got != tt.timeout {
					t.Errorf("%s timeout = %v, want %v", event, got, tt.timeout)
				}
			}
			if _, ok := document.Hooks[tt.eventKey]; !ok {
				t.Fatalf("snippet missing %s", tt.eventKey)
			}
		})
	}
}

func TestHookSnippetClaudeUsesPromptAndSubagentEvents(t *testing.T) {
	original := agentHookExecutable
	t.Cleanup(func() { agentHookExecutable = original })
	agentHookExecutable = func() (string, error) { return "/usr/local/bin/tmux-menu", nil }
	var out bytes.Buffer
	if err := runAgentHookSnippets([]string{"claude", "ingest"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`"UserPromptSubmit"`, `"Notification"`, `"matcher": "permission_prompt"`, `"PostToolUseFailure"`, `"PostToolBatch"`, `"PermissionDenied"`, `"StopFailure"`, `"SubagentStart"`, `agent-hook ingest claude`, `~/.claude/settings.json`} {
		if !strings.Contains(got, want) {
			t.Errorf("snippet missing %q", want)
		}
	}
}

func TestHookDoctorTraceMatrixShowsCoverageAndRejections(t *testing.T) {
	traces := []agentstatus.TraceEntry{
		{Provider: agentstatus.ProviderCodex, RawEvent: "SessionStart", Kind: agentstatus.EventSessionStart, Accepted: true},
		{Provider: agentstatus.ProviderCodex, RawEvent: "UserPromptSubmit", Kind: agentstatus.EventTurnStart, Accepted: true},
		{Provider: agentstatus.ProviderCodex, RawEvent: "PreToolUse", Kind: agentstatus.EventProgress, Accepted: true},
		{Provider: agentstatus.ProviderCodex, RawEvent: "PostToolUse", Kind: agentstatus.EventAttentionResolved, Accepted: true},
		{Provider: agentstatus.ProviderClaude, RawEvent: "PermissionDenied", Kind: agentstatus.EventFailure, Accepted: false},
		{Provider: agentstatus.ProviderClaude, RawEvent: "PostToolBatch", Kind: agentstatus.EventProgress, Accepted: true},
		{Provider: agentstatus.ProviderClaude, RawEvent: "SubagentStart", Kind: agentstatus.EventSubagentStart, Accepted: true},
	}
	var out bytes.Buffer
	printTraceMatrix(&out, traces)
	got := out.String()
	for _, want := range []string{
		"6 accepted, 1 rejected",
		"observed codex/session-start",
		"missing  codex/session-end",
		"observed codex/prompt",
		"observed codex/pre-tool",
		"observed codex/post-tool",
		"missing  claude/permission-denial",
		"observed claude/tool-batch",
		"missing  claude/permission-request",
		"observed claude/child-start",
		"missing  claude/child-stop",
		"missing  claude/stop",
		"manual   codex/parallel-tool",
		"manual   claude/approval-path",
		"manual   codex/interrupt-resume",
		"manual   claude/hard-kill",
		"unverified",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace matrix missing %q:\n%s", want, got)
		}
	}
}

func TestHookDoctorTraceMatrixDoesNotInferEdgesFromCollapsedMetadata(t *testing.T) {
	traces := []agentstatus.TraceEntry{
		{Provider: agentstatus.ProviderCodex, RawEvent: "StopFailure", Kind: agentstatus.EventTurnStop, Reason: "stop", Accepted: true},
		{Provider: agentstatus.ProviderCodex, RawEvent: "PostToolUseFailure", Kind: agentstatus.EventFailure, Reason: "post-tool", Accepted: true},
		{Provider: agentstatus.ProviderClaude, RawEvent: "Notification", Kind: agentstatus.EventAttentionConfirmed, Reason: "permission-request", Accepted: true},
		{Provider: agentstatus.ProviderClaude, Kind: agentstatus.EventSubagentStop, Reason: "SubagentStop", Accepted: true},
	}
	var out bytes.Buffer
	printTraceMatrix(&out, traces)
	got := out.String()
	for _, want := range []string{
		"missing  codex/post-tool",
		"missing  codex/stop",
		"missing  claude/permission-request",
		"missing  claude/child-stop",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace matrix overclaimed %q:\n%s", want, got)
		}
	}
}

func TestAgentPreviewSnapshotIsPrivateBoundedAndImmutable(t *testing.T) {
	children := make([]string, agentPreviewMaxChildren+4)
	for i := range children {
		children[i] = "child"
	}
	path, cleanup, err := writeAgentPreviewSnapshot(map[string]agentPreviewData{
		"%9": {Provider: "codex", State: "working", Source: "hook", Fresh: true, Children: children},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", got)
	}
	snapshot, err := readAgentPreviewSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(snapshot.Rows["%9"].Children); got != agentPreviewMaxChildren {
		t.Fatalf("children = %d, want %d", got, agentPreviewMaxChildren)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"rows":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Rows["%9"].State; got != "working" {
		t.Fatalf("already-read snapshot changed to %q", got)
	}
}

func TestAgentPreviewCommandShellQuotesOnlyFixedArguments(t *testing.T) {
	got := agentPreviewCommandForSnapshot("/tmp/menu app", "/tmp/snap'shot.json")
	want := `test -n {} && '/tmp/menu app' agent-hook preview '/tmp/snap'\''shot.json' {}`
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestAgentPreviewPrintsFrozenEvidenceThenTrimmedLiveScrollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := writeAgentPreviewSnapshotFile(path, agentPreviewSnapshot{Version: 1, Rows: map[string]agentPreviewData{
		"%4": {Provider: "claude", ProviderSession: "session-1234567890", State: "attention", Source: "hook", Event: "PermissionRequest", Reason: "permission", Age: "2s", Fresh: true, Children: []string{"worker | working"}},
	}}); err != nil {
		t.Fatal(err)
	}
	original := agentHookTmuxOutput
	t.Cleanup(func() { agentHookTmuxOutput = original })
	agentHookTmuxOutput = func(_ context.Context, args ...string) ([]byte, error) {
		if got := strings.Join(args, " "); got != "capture-pane -e -p -S -300 -t %4" {
			t.Fatalf("capture args = %q", args)
		}
		return []byte("line one\n\nline two\n\n\n"), nil
	}
	var out bytes.Buffer
	if err := runAgentPreview(context.Background(), path, "%4", &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "Status: claude | attention | fresh | 2s\nEvidence: hook (PermissionRequest)\nSession: session-12345678\n") {
		t.Fatalf("preview header = %q", got)
	}
	if !strings.Contains(got, "Child: worker | working\n\nline one\n\nline two\n") || strings.HasSuffix(got, "\n\n") {
		t.Fatalf("preview output not trimmed as expected: %q", got)
	}
}

func TestAgentPreviewSanitizesFrozenTerminalMetadata(t *testing.T) {
	var out bytes.Buffer
	printAgentPreviewHeader(&out, agentPreviewData{
		Provider: "codex\x1b[31m", State: "working\nforged", Source: "hook\rline", Reason: "safe\x00reason", Fresh: true,
	})
	got := out.String()
	if strings.ContainsAny(got, "\x1b\r\x00") || strings.Contains(got, "working\nforged") {
		t.Fatalf("preview metadata retained terminal controls: %q", got)
	}
}

func TestAgentPreviewRejectsUnsafePaneAndSnapshotSymlink(t *testing.T) {
	if err := runAgentPreview(context.Background(), "/tmp/unused", "%1; touch /tmp/oops", &bytes.Buffer{}); err == nil {
		t.Fatal("unsafe pane error = nil")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"version":1,"rows":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readAgentPreviewSnapshot(link); err == nil {
		t.Fatal("symlink snapshot error = nil")
	}
}

func TestInspectCodexHookConfigMergesHooksJSONAndInlineTOML(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "Tmux Menu", "tmux-menu")
	command := "'" + executable + "' agent-hook ingest codex"
	events := providerHookEvents("codex")
	if err := os.WriteFile(
		filepath.Join(codexDir, "hooks.json"),
		[]byte(providerHookJSON(t, events[:4], command)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(codexDir, "config.toml"),
		[]byte(providerHookTOML(t, events[4:], command)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	originalHomeDir := agentHookUserHomeDir
	t.Cleanup(func() { agentHookUserHomeDir = originalHomeDir })
	agentHookUserHomeDir = func() (string, error) { return dir, nil }

	inspection, err := inspectProviderHookConfigDetails("codex", executable)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Configured || !inspection.Current || !inspection.AnyCurrent || len(inspection.MissingEvents) != 0 {
		t.Fatalf("merged inspection = %+v, want complete current config", inspection)
	}
}

func TestInspectCodexHookConfigReadsInlineTOMLWhenHooksJSONExists(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := "/usr/local/bin/tmux-menu"
	command := executable + " agent-hook trace codex"
	events := providerHookEvents("codex")
	if err := os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(providerHookJSON(t, events[:1], command)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(providerHookTOML(t, events[1:], command)), 0o600); err != nil {
		t.Fatal(err)
	}
	originalHomeDir := agentHookUserHomeDir
	t.Cleanup(func() { agentHookUserHomeDir = originalHomeDir })
	agentHookUserHomeDir = func() (string, error) { return dir, nil }

	inspection, err := inspectProviderHookConfigDetails("codex", executable)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Configured || !inspection.AnyCurrent {
		t.Fatalf("inspectProviderHookConfigDetails = %+v; want merged configured/current", inspection)
	}
}

func TestInspectCodexHookConfigAcceptsInlineTOMLOnly(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := "/usr/local/bin/tmux-menu"
	command := executable + " agent-hook ingest codex"
	if err := os.WriteFile(
		filepath.Join(codexDir, "config.toml"),
		[]byte(providerHookTOML(t, providerHookEvents("codex"), command)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	originalHomeDir := agentHookUserHomeDir
	t.Cleanup(func() { agentHookUserHomeDir = originalHomeDir })
	agentHookUserHomeDir = func() (string, error) { return dir, nil }

	inspection, err := inspectProviderHookConfigDetails("codex", executable)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Configured || !inspection.Current || len(inspection.MissingEvents) != 0 {
		t.Fatalf("inline-only inspection = %+v, want complete current config", inspection)
	}
}

func TestInspectCodexHookConfigIgnoresCodexTrustState(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := "/usr/local/bin/tmux-menu"
	command := executable + " agent-hook trace codex"
	contents := providerHookTOML(t, providerHookEvents("codex"), command) + `
[hooks.state]
[hooks.state."/tmp/hooks.json:stop:0:0"]
trusted_hash = "sha256:test"
`
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	originalHomeDir := agentHookUserHomeDir
	t.Cleanup(func() { agentHookUserHomeDir = originalHomeDir })
	agentHookUserHomeDir = func() (string, error) { return dir, nil }

	inspection, err := inspectProviderHookConfigDetails("codex", executable)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Configured || !inspection.Current || len(inspection.MissingEvents) != 0 {
		t.Fatalf("inspection with trust state = %+v, want complete current config", inspection)
	}
}

func TestHookDoctorReportsReadOnlyDiagnosticsWithoutSensitiveValues(t *testing.T) {
	dir := t.TempDir()
	stateRoot := filepath.Join(dir, "state")
	bin := filepath.Join(dir, "tmux-menu")
	if err := os.WriteFile(bin, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_MENU_AGENT_STATE_DIR", stateRoot)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	originalExecutable := agentHookExecutable
	originalLookPath := agentHookLookPath
	originalCommandOutput := agentHookCommandOutput
	originalHomeDir := agentHookUserHomeDir
	t.Cleanup(func() {
		agentHookExecutable = originalExecutable
		agentHookLookPath = originalLookPath
		agentHookCommandOutput = originalCommandOutput
		agentHookUserHomeDir = originalHomeDir
	})
	agentHookExecutable = func() (string, error) { return bin, nil }
	agentHookUserHomeDir = func() (string, error) { return dir, nil }
	for _, configPath := range []string{filepath.Join(dir, ".codex", "hooks.json"), filepath.Join(dir, ".claude", "settings.json")} {
		if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
			t.Fatal(err)
		}
		provider := "codex"
		if strings.Contains(configPath, ".claude") {
			provider = "claude"
		}
		document := providerHookJSON(t, providerHookEvents(provider), "'"+bin+"' agent-hook ingest "+provider)
		var withPrivate map[string]any
		if err := json.Unmarshal([]byte(document), &withPrivate); err != nil {
			t.Fatal(err)
		}
		withPrivate["private_note"] = "DO_NOT_PRINT_SECRET"
		encoded, err := json.Marshal(withPrivate)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	agentHookLookPath = func(file string) (string, error) {
		if file == "codex" || file == "claude" || file == "tmux" {
			return "/safe/" + file, nil
		}
		return "", errors.New("not found")
	}
	agentHookCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		return []byte(filepath.Base(name) + " 1.2.3\n"), nil
	}
	var out bytes.Buffer
	if err := runAgentHookDoctor(context.Background(), &out); err != nil {
		t.Fatalf("doctor error = %v; output:\n%s", err, out.String())
	}
	if _, err := os.Stat(stateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only doctor created state root: %v", err)
	}
	got := out.String()
	for _, want := range []string{"tmux-menu binary is available", "codex provider is installed", "codex version: codex 1.2.3", "codex tmux-menu hook is configured", "outside tmux", "state directory does not exist yet", "missing  codex/permission-request", "manual   codex/approval-path", "no trace coverage yet"} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, dir) || strings.Contains(got, "DO_NOT_PRINT_SECRET") {
		t.Fatalf("doctor leaked state, binary, or config values: %s", got)
	}
}

func providerHookJSON(t *testing.T, events []string, command string) string {
	t.Helper()
	hooks := make(map[string]any, len(events))
	for _, event := range events {
		hooks[event] = []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": command}},
		}}
	}
	encoded, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func providerHookTOML(t *testing.T, events []string, command string) string {
	t.Helper()
	encodedCommand, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	out.WriteString("[hooks]\n")
	for _, event := range events {
		out.WriteString(event)
		out.WriteString(" = [{ hooks = [{ type = \"command\", command = ")
		out.Write(encodedCommand)
		out.WriteString(" }] }]\n")
	}
	return out.String()
}
