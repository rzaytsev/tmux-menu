package agentstatus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreCompletionAcknowledgementIsCompareAndSet(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	ctx := context.Background()
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	applyCore(t, store, coreEvent(EventTurnStart, "turn-1", now))
	first := applyCore(t, store, coreEvent(EventTurnStop, "turn-1", now.Add(time.Second)))
	if first.State != StateCompleted || first.AcknowledgeToken == "" {
		t.Fatalf("first completion = %+v", first)
	}

	applyCore(t, store, coreEvent(EventTurnStart, "turn-2", now.Add(2*time.Second)))
	second := applyCore(t, store, coreEvent(EventTurnStop, "turn-2", now.Add(3*time.Second)))
	if second.State != StateCompleted || second.AcknowledgeToken == "" || second.AcknowledgeToken == first.AcknowledgeToken {
		t.Fatalf("new completion token did not advance: first=%+v second=%+v", first, second)
	}

	acknowledged, err := store.Acknowledge(ctx, first.AcknowledgeToken, now.Add(4*time.Second))
	if acknowledged || !errors.Is(err, ErrNotAcknowledgeable) {
		t.Fatalf("stale token acknowledgement = %v, %v", acknowledged, err)
	}
	annotations, problems := store.Snapshot(ctx, []LivePane{{Pane: coreTestPane()}}, now.Add(4*time.Second))
	if len(problems) != 0 || len(annotations) != 1 || annotations[0].TurnID != "turn-2" || annotations[0].State != StateCompleted {
		t.Fatalf("stale acknowledgement damaged newer completion: annotations=%+v problems=%v", annotations, problems)
	}

	acknowledged, err = store.Acknowledge(ctx, second.AcknowledgeToken, now.Add(5*time.Second))
	if err != nil || !acknowledged {
		t.Fatalf("current token acknowledgement = %v, %v", acknowledged, err)
	}
	annotations, problems = store.Snapshot(ctx, []LivePane{{Pane: coreTestPane()}}, now.Add(5*time.Second))
	if len(problems) != 0 || len(annotations) != 0 {
		t.Fatalf("acknowledged completion remained authoritative: annotations=%+v problems=%v", annotations, problems)
	}
	recordPath := store.recordPath(recordKey(coreTestPane(), ProviderClaude))
	current, err := readRecord(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != StateWaiting || current.Reason != "completion-acknowledged" {
		t.Fatalf("acknowledged record = %+v", current)
	}
	acknowledged, err = store.Acknowledge(ctx, second.AcknowledgeToken, now.Add(6*time.Second))
	if acknowledged || !errors.Is(err, ErrNotAcknowledgeable) {
		t.Fatalf("replayed token acknowledgement = %v, %v", acknowledged, err)
	}
}

func TestStoreNeverAcknowledgesAttention(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	applyCore(t, store, coreEvent(EventTurnStart, "turn-1", now))
	wait := coreEvent(EventAttentionConfirmed, "turn-1", now.Add(time.Second))
	wait.CorrelationID = "id:question"
	annotation := applyCore(t, store, wait)
	if annotation.State != StateAttention || annotation.AcknowledgeToken != "" {
		t.Fatalf("attention annotation = %+v", annotation)
	}
	acknowledged, err := store.Acknowledge(context.Background(), strings.Repeat("0", 64), now.Add(2*time.Second))
	if acknowledged || !errors.Is(err, ErrNotAcknowledgeable) {
		t.Fatalf("attention acknowledgement = %v, %v", acknowledged, err)
	}
	annotations, problems := store.Snapshot(context.Background(), []LivePane{{Pane: coreTestPane()}}, now.Add(2*time.Second))
	if len(problems) != 0 || len(annotations) != 1 || annotations[0].State != StateAttention {
		t.Fatalf("attention changed after acknowledgement attempt: annotations=%+v problems=%v", annotations, problems)
	}
}

func TestSnapshotReadOnlyDoesNotReconcileMismatchedRecords(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	event := coreEvent(EventTurnStart, "turn-1", now)
	applyCore(t, store, event)
	path := store.recordPath(recordKey(event.Pane, event.Provider))

	live := event.Pane
	live.ProviderPID++
	annotations, problems := store.SnapshotReadOnly(context.Background(), []LivePane{{Pane: live, Provider: event.Provider}}, now.Add(time.Second), nil)
	if len(annotations) != 0 || len(problems) != 0 {
		t.Fatalf("read-only mismatch = %+v, problems=%v", annotations, problems)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("read-only snapshot removed hook record: %v", err)
	}
}

func TestSnapshotReadOnlyOnlyReadsBoundedLiveRecordPaths(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	event := coreEvent(EventTurnStart, "turn-1", now)
	applyCore(t, store, event)
	if err := os.WriteFile(filepath.Join(store.root, strings.Repeat("f", 64)+recordSuffix), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	reserved := 0
	consume := func(size int) bool {
		reserved += size
		return reserved <= maxStateFile
	}
	annotations, problems := store.SnapshotReadOnly(context.Background(), []LivePane{{Pane: event.Pane, Provider: event.Provider}}, now.Add(time.Second), consume)
	if len(problems) != 0 || len(annotations) != 1 || reserved <= 0 || reserved > maxStateFile {
		t.Fatalf("bounded read-only snapshot = %+v, problems=%v reserved=%d", annotations, problems, reserved)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	annotations, problems = store.SnapshotReadOnly(canceled, []LivePane{{Pane: event.Pane, Provider: event.Provider}}, now.Add(time.Second), consume)
	if len(annotations) != 0 || len(problems) != 1 || !errors.Is(problems[0], context.Canceled) {
		t.Fatalf("canceled read-only snapshot = %+v, problems=%v", annotations, problems)
	}
}

func TestStoreChildCompletionRollupIsCASAcknowledgeable(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	ctx := context.Background()
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	applyCore(t, store, coreEvent(EventSessionStart, "", now))
	childStart := coreEvent(EventSubagentStart, "turn-1", now.Add(time.Second))
	childStart.ChildID = "child-1"
	childStart.ChildType = "worker"
	applyCore(t, store, childStart)
	childStop := coreEvent(EventSubagentStop, "turn-1", now.Add(2*time.Second))
	childStop.ChildID = "child-1"
	first := applyCore(t, store, childStop)
	if first.State != StateCompleted || first.AcknowledgeToken == "" || len(first.Children) != 1 || first.Children[0].State != StateCompleted {
		t.Fatalf("completed child rollup is not acknowledgeable: %+v", first)
	}

	secondStart := coreEvent(EventSubagentStart, "turn-1", now.Add(3*time.Second))
	secondStart.ChildID = "child-2"
	applyCore(t, store, secondStart)
	secondStop := coreEvent(EventSubagentStop, "turn-1", now.Add(4*time.Second))
	secondStop.ChildID = "child-2"
	second := applyCore(t, store, secondStop)
	if second.State != StateCompleted || second.AcknowledgeToken == "" || second.AcknowledgeToken == first.AcknowledgeToken {
		t.Fatalf("new child completion did not advance token: first=%+v second=%+v", first, second)
	}

	acknowledged, err := store.Acknowledge(ctx, first.AcknowledgeToken, now.Add(5*time.Second))
	if acknowledged || !errors.Is(err, ErrNotAcknowledgeable) {
		t.Fatalf("stale child token acknowledgement = %v, %v", acknowledged, err)
	}
	annotations, problems := store.Snapshot(ctx, []LivePane{{Pane: coreTestPane(), Provider: ProviderClaude}}, now.Add(5*time.Second))
	if len(problems) != 0 || len(annotations) != 1 || annotations[0].State != StateCompleted || annotations[0].AcknowledgeToken != second.AcknowledgeToken {
		t.Fatalf("stale child token cleared newer completion: annotations=%+v problems=%v", annotations, problems)
	}

	acknowledged, err = store.Acknowledge(ctx, second.AcknowledgeToken, now.Add(6*time.Second))
	if err != nil || !acknowledged {
		t.Fatalf("current child completion acknowledgement = %v, %v", acknowledged, err)
	}
	annotations, problems = store.Snapshot(ctx, []LivePane{{Pane: coreTestPane(), Provider: ProviderClaude}}, now.Add(6*time.Second))
	if len(problems) != 0 {
		t.Fatalf("snapshot problems after child acknowledgement = %v", problems)
	}
	for _, annotation := range annotations {
		if annotation.State == StateCompleted || annotation.AcknowledgeToken != "" {
			t.Fatalf("acknowledged child completion remained queued: %+v", annotation)
		}
	}
	current, err := readRecord(store.recordPath(recordKey(coreTestPane(), ProviderClaude)))
	if err != nil {
		t.Fatal(err)
	}
	acknowledgedAt := now.Add(6 * time.Second)
	if !current.StateChangedAt.Equal(acknowledgedAt) || !current.LastEventAt.Equal(acknowledgedAt) {
		t.Fatalf("child-rollup acknowledgement timing = %+v", current)
	}
	acknowledged, err = store.Acknowledge(ctx, second.AcknowledgeToken, now.Add(7*time.Second))
	if acknowledged || !errors.Is(err, ErrNotAcknowledgeable) {
		t.Fatalf("replayed child completion token = %v, %v", acknowledged, err)
	}
}

func TestStoreConcurrentChildStartsDoNotLoseUpdates(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	applyCore(t, store, coreEvent(EventTurnStart, "turn-1", now))

	const children = 40
	start := make(chan struct{})
	errs := make(chan error, children)
	var wg sync.WaitGroup
	for i := 0; i < children; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			event := coreEvent(EventSubagentStart, "turn-1", now.Add(time.Duration(i+1)*time.Millisecond))
			event.ChildID = fmt.Sprintf("child-%02d", i)
			event.ChildType = "worker"
			_, decision, err := store.Apply(context.Background(), event)
			if err != nil {
				errs <- err
				return
			}
			if !decision.Applied {
				errs <- fmt.Errorf("child %d decision = %+v", i, decision)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	annotations, problems := store.Snapshot(context.Background(), []LivePane{{Pane: coreTestPane()}}, now.Add(time.Second))
	if len(problems) != 0 || len(annotations) != 1 {
		t.Fatalf("snapshot annotations=%+v problems=%v", annotations, problems)
	}
	if got := len(annotations[0].Children); got != children {
		t.Fatalf("concurrent child count = %d, want %d; children=%+v", got, children, annotations[0].Children)
	}
}

func TestSnapshotNeverCreatesRowsOrStorage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing", "agent-state")
	store, err := NewStore(root, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	annotations, problems := store.Snapshot(context.Background(), []LivePane{{Pane: coreTestPane()}}, time.Now())
	if len(annotations) != 0 || len(problems) != 0 {
		t.Fatalf("snapshot without records = %+v, %v", annotations, problems)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("read-only snapshot created root: %v", err)
	}
}

func TestSnapshotReturnsAtMostOneProviderAnnotationPerLivePane(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	claude := coreEvent(EventSessionStart, "", now)
	claude.Provider = ProviderClaude
	claude.ProviderSessionID = "claude-session"
	applyCore(t, store, claude)
	codex := coreEvent(EventTurnStop, "codex-turn", now.Add(time.Second))
	codex.Provider = ProviderCodex
	codex.ProviderSessionID = "codex-session"
	applyCore(t, store, codex)

	annotations, problems := store.Snapshot(context.Background(), []LivePane{{Pane: coreTestPane(), Provider: ProviderClaude}}, now.Add(2*time.Second))
	if len(problems) != 0 {
		t.Fatalf("provider-restricted snapshot problems = %v", problems)
	}
	if len(annotations) != 1 || annotations[0].Provider != ProviderClaude || annotations[0].ProviderSessionID != "claude-session" {
		t.Fatalf("detected Claude did not select its older exact record: %+v", annotations)
	}

	// A provider-restricted reconciliation may retire the opposite claim. Apply
	// the newer record again so the unrestricted selection is an independent
	// assertion over two exact live records.
	applyCore(t, store, codex)
	annotations, problems = store.Snapshot(context.Background(), []LivePane{{Pane: coreTestPane()}}, now.Add(2*time.Second))
	if len(problems) != 0 {
		t.Fatalf("unrestricted snapshot problems = %v", problems)
	}
	if len(annotations) != 1 || annotations[0].Provider != ProviderCodex || annotations[0].ProviderSessionID != "codex-session" {
		t.Fatalf("unrestricted snapshot did not select newest record deterministically: %+v", annotations)
	}
}

func TestSnapshotReconcilesEveryPaneIdentityMismatch(t *testing.T) {
	tests := []struct {
		name string
		live PaneIdentity
	}{
		{name: "server", live: mutatePane(func(p *PaneIdentity) { p.ServerID = "other-server" })},
		{name: "pane", live: mutatePane(func(p *PaneIdentity) { p.PaneID = "%999" })},
		{name: "pid", live: mutatePane(func(p *PaneIdentity) { p.PanePID++ })},
		{name: "provider pid", live: mutatePane(func(p *PaneIdentity) { p.ProviderPID++ })},
		{name: "tmux session", live: mutatePane(func(p *PaneIdentity) { p.TmuxSessionID = "$999" })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newCoreTestStore(t, testPolicy())
			now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
			applyCore(t, store, coreEvent(EventSessionStart, "", now))
			path := store.recordPath(recordKey(coreTestPane(), ProviderClaude))
			if _, err := os.Lstat(path); err != nil {
				t.Fatal(err)
			}
			annotations, problems := store.Snapshot(context.Background(), []LivePane{{Pane: tt.live}}, now.Add(time.Second))
			if len(annotations) != 0 || len(problems) != 0 {
				t.Fatalf("mismatched snapshot = %+v, problems=%v", annotations, problems)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("mismatched record not reconciled: %v", err)
			}
		})
	}
}

func TestSnapshotPreservesProviderClaimWhenProcessInventoryIsUnavailable(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	event := coreEvent(EventTurnStart, "turn-1", now)
	event.Pane.ProviderPID = 4200
	applyCore(t, store, event)

	unknownProcessInventory := event.Pane
	unknownProcessInventory.ProviderPID = 0
	annotations, problems := store.Snapshot(context.Background(), []LivePane{{Pane: unknownProcessInventory, Provider: event.Provider}}, now.Add(time.Second))
	if len(problems) != 0 || len(annotations) != 1 || annotations[0].Pane.ProviderPID != 4200 {
		t.Fatalf("unavailable process inventory retired claim: annotations=%+v problems=%v", annotations, problems)
	}

	confirmedMissingProvider := event.Pane
	confirmedMissingProvider.ProviderPID = -1
	annotations, problems = store.Snapshot(context.Background(), []LivePane{{Pane: confirmedMissingProvider, Provider: event.Provider}}, now.Add(2*time.Second))
	if len(problems) != 0 || len(annotations) != 0 {
		t.Fatalf("confirmed provider exit retained claim: annotations=%+v problems=%v", annotations, problems)
	}
}

func TestStoreCreatesPrivateDirectoriesAndAtomicFiles(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	applyCore(t, store, coreEvent(EventSessionStart, "", now))

	info, err := os.Lstat(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("state root mode = %04o, want 0700", got)
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		t.Fatal(err)
	}
	records := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-agent-status-") {
			t.Fatalf("atomic temp file leaked: %s", entry.Name())
		}
		if !validRecordFilename(entry.Name()) {
			continue
		}
		records++
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("record %s mode = %v, want regular 0600", entry.Name(), info.Mode())
		}
		if strings.Contains(entry.Name(), coreTestPane().PaneID) || strings.Contains(entry.Name(), string(ProviderClaude)) {
			t.Fatalf("record path exposes identity: %s", entry.Name())
		}
	}
	if records != 1 {
		t.Fatalf("record files = %d, want 1; entries=%v", records, entries)
	}
}

func TestStoreAtomicReadsDuringConcurrentReplacement(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	applyCore(t, store, coreEvent(EventTurnStart, "turn-1", now))

	const iterations = 80
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			event := coreEvent(EventProgress, "turn-1", now.Add(time.Duration(i+1)*time.Millisecond))
			if _, _, err := store.Apply(context.Background(), event); err != nil {
				errs <- fmt.Errorf("apply %d: %w", i, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			annotations, problems := store.Snapshot(context.Background(), []LivePane{{Pane: coreTestPane()}}, now.Add(time.Second))
			if len(problems) != 0 {
				errs <- fmt.Errorf("snapshot %d: %v", i, problems)
				return
			}
			if len(annotations) != 1 {
				errs <- fmt.Errorf("snapshot %d annotation count = %d", i, len(annotations))
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-agent-status-") {
			t.Fatalf("atomic replacement leaked temp file: %s", entry.Name())
		}
	}
}

func TestStoreRejectsSymlinkRootAndRecord(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "state-link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := NewStore(link, testPolicy()); err == nil {
			t.Fatal("NewStore accepted a symlink root")
		}
	})

	t.Run("record", func(t *testing.T) {
		store := newCoreTestStore(t, testPolicy())
		now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
		applyCore(t, store, coreEvent(EventSessionStart, "", now))
		path := store.recordPath(recordKey(coreTestPane(), ProviderClaude))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "target.json")
		if err := os.WriteFile(target, []byte(`{"sentinel":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		_, _, err := store.Apply(context.Background(), coreEvent(EventSessionStart, "", now.Add(time.Second)))
		if err == nil {
			t.Fatal("Apply followed a symlink record")
		}
		annotations, problems := store.Snapshot(context.Background(), []LivePane{{Pane: coreTestPane()}}, now.Add(time.Second))
		if len(annotations) != 0 || len(problems) != 1 {
			t.Fatalf("symlink snapshot = %+v, problems=%v", annotations, problems)
		}
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `{"sentinel":true}` {
			t.Fatalf("symlink target mutated: %s", got)
		}
	})
}

func TestPersistedStateAndTracesExcludeSensitiveHookPayload(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	const secret = "SUPER_PRIVATE_AGENT_PAYLOAD"
	payload := `{"hook_event_name":"PreToolUse","session_id":"session-safe","turn_id":"turn-safe","tool_name":"AskUserQuestion","tool_input":{"question":"` + secret + `"},"prompt":"` + secret + `","cwd":"` + secret + `","transcript_path":"` + secret + `"}`
	event, trace, err := DecodeHook(ProviderClaude, coreTestPane(), strings.NewReader(payload), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Apply(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTrace(context.Background(), trace); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(store.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().Perm() != 0o700 {
				return fmt.Errorf("directory %s mode %04o", path, info.Mode().Perm())
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0o600 {
			return fmt.Errorf("file %s mode %04o", path, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), secret) {
			return fmt.Errorf("sensitive payload persisted in %s: %s", path, data)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTraceRetentionIsBoundedAndNewestFirst(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	base := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	for i := 0; i < traceLimit+3; i++ {
		trace := TraceEntry{
			Provider:          ProviderClaude,
			ProviderSessionID: "provider-session-1",
			TurnID:            "turn-1",
			Pane:              coreTestPane(),
			RawEvent:          fmt.Sprintf("event-%03d", i),
			Kind:              EventProgress,
			Reason:            "progress",
			ObservedAt:        base.Add(time.Duration(i) * time.Nanosecond),
			Accepted:          true,
		}
		if err := store.AppendTrace(context.Background(), trace); err != nil {
			t.Fatalf("append trace %d: %v", i, err)
		}
	}
	traces, err := store.RecentTraces(context.Background(), traceLimit+100)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != traceLimit {
		t.Fatalf("trace count = %d, want %d", len(traces), traceLimit)
	}
	if got, want := traces[0].RawEvent, fmt.Sprintf("event-%03d", traceLimit+2); got != want {
		t.Fatalf("newest trace = %q, want %q", got, want)
	}
	if got, want := traces[len(traces)-1].RawEvent, "event-003"; got != want {
		t.Fatalf("oldest retained trace = %q, want %q", got, want)
	}
	latest, err := store.RecentTraces(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 3 || latest[0].RawEvent != fmt.Sprintf("event-%03d", traceLimit+2) || latest[2].RawEvent != fmt.Sprintf("event-%03d", traceLimit) {
		t.Fatalf("latest traces = %+v", latest)
	}
}

func TestTraceSchemaIsValidatedBeforeWriteAndOnRead(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	store, err := NewStore(root, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	invalid := TraceEntry{
		Provider:   ProviderClaude,
		Pane:       coreTestPane(),
		RawEvent:   "PreToolUse",
		Kind:       EventProgress,
		ObservedAt: time.Now(),
		Accepted:   true,
	}
	if err := store.AppendTrace(context.Background(), invalid); err == nil {
		t.Fatal("AppendTrace accepted a trace without a provider session")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("invalid trace created state root: %v", err)
	}

	valid := invalid
	valid.ProviderSessionID = "provider-session-1"
	if err := store.AppendTrace(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "traces"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			path := filepath.Join(root, "traces", entry.Name())
			if err := os.WriteFile(path, []byte(`{"provider":"claude","accepted":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if _, err := store.RecentTraces(context.Background(), traceLimit); err == nil {
		t.Fatal("RecentTraces accepted an incompatible persisted trace")
	}
	if err := store.ValidateReadOnly(context.Background()); err == nil {
		t.Fatal("ValidateReadOnly accepted an incompatible persisted trace")
	}
}

func TestConstructorAndTraceReadsDoNotCreateStorage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing", "state")
	store, err := NewStore(root, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("constructor created storage: %v", err)
	}
	traces, err := store.RecentTraces(context.Background(), 10)
	if err != nil || len(traces) != 0 {
		t.Fatalf("empty RecentTraces = %+v, %v", traces, err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("RecentTraces created storage: %v", err)
	}
	traces, err = store.RecentTraces(context.Background(), 0)
	if err != nil || traces != nil {
		t.Fatalf("zero RecentTraces = %+v, %v", traces, err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("zero RecentTraces created storage: %v", err)
	}
}

func TestStoreLockWaitIsBounded(t *testing.T) {
	policy := Policy{WorkingTTL: time.Second, ChildRetention: time.Minute, LockTimeout: 30 * time.Millisecond}
	store := newCoreTestStore(t, policy)
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	applyCore(t, store, coreEvent(EventSessionStart, "", now))
	lockPath := filepath.Join(store.root, ".lock-"+hashKey(recordKey(coreTestPane(), ProviderClaude)))
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, _, err := store.Apply(context.Background(), coreEvent(EventTurnStart, "turn-1", now.Add(time.Second)))
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("contended apply error = %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("bounded lock took %s", elapsed)
	}
}

func newCoreTestStore(t *testing.T, policy Policy) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "agent-state")
	store, err := NewStore(root, policy)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func applyCore(t *testing.T, store *Store, event Event) Annotation {
	t.Helper()
	annotation, decision, err := store.Apply(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Applied {
		t.Fatalf("event %+v decision = %+v", event, decision)
	}
	return annotation
}

func mutatePane(change func(*PaneIdentity)) PaneIdentity {
	pane := coreTestPane()
	change(&pane)
	return pane
}
