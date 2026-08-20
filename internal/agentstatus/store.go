package agentstatus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	recordSuffix = ".json"
	traceLimit   = 512
	maxStateFile = 1 << 20
)

type Store struct {
	root   string
	policy Policy
}

func NewStore(root string, policy Policy) (*Store, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("agent status root must be absolute")
	}
	if policy.WorkingTTL <= 0 || policy.ChildRetention <= 0 || policy.LockTimeout <= 0 {
		return nil, errors.New("agent status policy durations must be positive")
	}
	if err := verifyRootIfPresent(root); err != nil {
		return nil, err
	}
	return &Store{root: root, policy: policy}, nil
}

func (s *Store) Apply(ctx context.Context, event Event) (Annotation, Decision, error) {
	if err := validateEvent(event); err != nil {
		return Annotation{}, Decision{}, err
	}
	if err := s.ensureRoot(); err != nil {
		return Annotation{}, Decision{}, err
	}
	key := recordKey(event.Pane, event.Provider)
	unlock, err := s.lock(ctx, key)
	if err != nil {
		return Annotation{}, Decision{}, err
	}
	defer unlock()
	path := s.recordPath(key)
	previous, err := readRecord(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Annotation{}, Decision{}, err
	}
	if err == nil && !samePane(previous.Pane, event.Pane) {
		previous = record{}
	}
	next, decision := reduce(previous, event, s.policy)
	if decision.Applied {
		if err := writeJSONAtomic(path, next); err != nil {
			return Annotation{}, decision, err
		}
	}
	return resolve(next, event.ObservedAt, s.policy), decision, nil
}

func (s *Store) Snapshot(ctx context.Context, live []LivePane, now time.Time) ([]Annotation, []error) {
	if len(live) == 0 {
		return nil, nil
	}
	if _, err := os.Lstat(s.root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, []error{err}
	}
	if err := verifyRootIfPresent(s.root); err != nil {
		return nil, []error{err}
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{err}
	}
	liveByKey := make(map[string]PaneIdentity, len(live)*2)
	liveProviderByPane := make(map[string]Provider, len(live))
	for _, item := range live {
		if item.Pane.ServerID == "" || item.Pane.PaneID == "" {
			continue
		}
		if item.Provider == "" || item.Provider == ProviderCodex {
			liveByKey[recordKey(item.Pane, ProviderCodex)] = item.Pane
		}
		if item.Provider == "" || item.Provider == ProviderClaude {
			liveByKey[recordKey(item.Pane, ProviderClaude)] = item.Pane
		}
		liveProviderByPane[item.Pane.ServerID+"\x00"+item.Pane.PaneID] = item.Provider
	}
	annotationsByPane := make(map[string]Annotation, len(live))
	var problems []error
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !validRecordFilename(name) {
			continue
		}
		path := filepath.Join(s.root, name)
		current, readErr := readRecord(path)
		if readErr != nil {
			problems = append(problems, fmt.Errorf("read agent state %s: %w", name, readErr))
			continue
		}
		key := recordKey(current.Pane, current.Provider)
		livePane, ok := liveByKey[key]
		if !ok || !sameLivePane(current.Pane, livePane) || hashKey(key)+recordSuffix != name {
			if removeErr := s.removeRecordIfStillOrphan(ctx, key, path, liveByKey); removeErr != nil {
				problems = append(problems, removeErr)
			}
			continue
		}
		annotation := resolve(current, now, s.policy)
		if annotation.State == StateUnknown && !annotation.Fresh && len(annotation.Children) == 0 {
			continue
		}
		paneKey := annotation.Pane.ServerID + "\x00" + annotation.Pane.PaneID
		selected, exists := annotationsByPane[paneKey]
		preferred := liveProviderByPane[paneKey]
		if !exists || annotation.Provider == preferred && selected.Provider != preferred ||
			(annotation.Provider == selected.Provider || preferred == "") && annotation.UpdatedAt.After(selected.UpdatedAt) ||
			annotation.UpdatedAt.Equal(selected.UpdatedAt) && annotation.Provider < selected.Provider {
			annotationsByPane[paneKey] = annotation
		}
	}
	annotations := make([]Annotation, 0, len(annotationsByPane))
	for _, annotation := range annotationsByPane {
		annotations = append(annotations, annotation)
	}
	sort.SliceStable(annotations, func(i, j int) bool { return annotations[i].Pane.PaneID < annotations[j].Pane.PaneID })
	return annotations, problems
}

func (s *Store) Acknowledge(ctx context.Context, token string, now time.Time) (bool, error) {
	token = strings.TrimSpace(token)
	if len(token) != 64 {
		return false, ErrNotAcknowledgeable
	}
	if _, err := os.Lstat(s.root); err != nil {
		return false, err
	}
	if err := verifyRootIfPresent(s.root); err != nil {
		return false, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !validRecordFilename(entry.Name()) {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		candidate, readErr := readRecord(path)
		if readErr != nil {
			continue
		}
		candidateAnnotation := resolve(candidate, now, s.policy)
		if candidateAnnotation.State != StateCompleted || candidateAnnotation.AcknowledgeToken != token {
			continue
		}
		key := recordKey(candidate.Pane, candidate.Provider)
		unlock, lockErr := s.lock(ctx, key)
		if lockErr != nil {
			return false, lockErr
		}
		current, readErr := readRecord(path)
		if readErr != nil {
			unlock()
			return false, readErr
		}
		currentAnnotation := resolve(current, now, s.policy)
		if currentAnnotation.State != StateCompleted || currentAnnotation.AcknowledgeToken != token {
			unlock()
			return false, ErrNotAcknowledgeable
		}
		current.Sequence++
		if current.State == StateCompleted {
			current.State = StateWaiting
		}
		current.StateChangedAt = now
		current.Reason = "completion-acknowledged"
		current.RawEvent = "acknowledge"
		current.UpdatedAt = now
		current.LastEventAt = now
		for id, child := range current.Children {
			if child.State == StateCompleted {
				delete(current.Children, id)
			}
		}
		writeErr := writeJSONAtomic(path, current)
		unlock()
		if writeErr != nil {
			return false, writeErr
		}
		return true, nil
	}
	return false, ErrNotAcknowledgeable
}

func (s *Store) AppendTrace(ctx context.Context, trace TraceEntry) error {
	if err := validateTrace(trace); err != nil {
		return err
	}
	if err := s.ensureRoot(); err != nil {
		return err
	}
	traceDir := filepath.Join(s.root, "traces")
	if err := ensurePrivateDir(traceDir); err != nil {
		return err
	}
	unlock, err := s.lockNamed(ctx, "traces")
	if err != nil {
		return err
	}
	defer unlock()
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", trace.ObservedAt.UnixNano(), hex.EncodeToString(nonce[:]))
	if err := writeJSONAtomic(filepath.Join(traceDir, name), trace); err != nil {
		return err
	}
	entries, err := os.ReadDir(traceDir)
	if err != nil {
		return err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	for len(files) > traceLimit {
		path := filepath.Join(traceDir, files[0])
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			_ = os.Remove(path)
		}
		files = files[1:]
	}
	return nil
}

func (s *Store) RecentTraces(ctx context.Context, limit int) ([]TraceEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	if limit > traceLimit {
		limit = traceLimit
	}
	if _, err := os.Lstat(s.root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := verifyRootIfPresent(s.root); err != nil {
		return nil, err
	}
	traceDir := filepath.Join(s.root, "traces")
	info, err := os.Lstat(traceDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("unsafe trace directory")
	}
	entries, err := os.ReadDir(traceDir)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	if len(files) > limit {
		files = files[:limit]
	}
	traces := make([]TraceEntry, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var trace TraceEntry
		if err := readJSONFile(filepath.Join(traceDir, file), &trace); err != nil {
			return nil, err
		}
		if err := validateTrace(trace); err != nil {
			return nil, err
		}
		traces = append(traces, trace)
	}
	return traces, nil
}

// ValidateReadOnly checks the persisted schema and permissions without
// creating directories, locks, temporary files, or reconciliation writes.
func (s *Store) ValidateReadOnly(ctx context.Context) error {
	if _, err := os.Lstat(s.root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := verifyRootIfPresent(s.root); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if name == "traces" || strings.HasPrefix(name, ".lock-") {
				continue
			}
			return fmt.Errorf("unexpected agent status directory %q", name)
		}
		if !validRecordFilename(name) {
			continue
		}
		current, err := readRecord(filepath.Join(s.root, name))
		if err != nil {
			return err
		}
		if hashKey(recordKey(current.Pane, current.Provider))+recordSuffix != name {
			return errors.New("agent status record key does not match its filename")
		}
	}
	_, err = s.RecentTraces(ctx, traceLimit)
	return err
}

func (s *Store) removeRecordIfStillOrphan(ctx context.Context, key, path string, liveByKey map[string]PaneIdentity) error {
	unlock, err := s.lock(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	current, err := readRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	currentKey := recordKey(current.Pane, current.Provider)
	if livePane, ok := liveByKey[currentKey]; ok && sameLivePane(current.Pane, livePane) && hashKey(currentKey)+recordSuffix == filepath.Base(path) {
		return nil
	}
	return os.Remove(path)
}

func (s *Store) recordPath(key string) string {
	return filepath.Join(s.root, hashKey(key)+recordSuffix)
}
func recordKey(pane PaneIdentity, provider Provider) string {
	return pane.ServerID + "\x00" + pane.PaneID + "\x00" + string(provider)
}
func hashKey(key string) string { sum := sha256.Sum256([]byte(key)); return hex.EncodeToString(sum[:]) }
func validRecordFilename(name string) bool {
	if len(name) != 64+len(recordSuffix) || !strings.HasSuffix(name, recordSuffix) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimSuffix(name, recordSuffix))
	return err == nil
}

func (s *Store) ensureRoot() error { return ensurePrivateDir(s.root) }
func ensurePrivateDir(path string) error {
	if err := verifyNoSymlinkParents(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe agent status directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("agent status directory is not private")
	}
	return nil
}

func verifyRootIfPresent(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("unsafe agent status root")
	}
	return nil
}

func verifyNoSymlinkParents(path string) error {
	// Reject a symlink at the application-owned root itself. Platform paths
	// such as macOS /var -> /private/var may legitimately contain symlinked
	// ancestors outside tmux-menu's ownership.
	info, err := os.Lstat(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink agent status root: %s", path)
	}
	return nil
}

func (s *Store) lock(ctx context.Context, key string) (func(), error) {
	return s.lockNamed(ctx, hashKey(key))
}
func (s *Store) lockNamed(ctx context.Context, name string) (func(), error) {
	lockPath := filepath.Join(s.root, ".lock-"+name)
	deadline := time.Now().Add(s.policy.LockTimeout)
	staleAfter := 30 * time.Second
	if candidate := s.policy.LockTimeout * 20; candidate > staleAfter {
		staleAfter = candidate
	}
	for {
		if err := os.Mkdir(lockPath, 0o700); err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, err := os.Lstat(lockPath); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && time.Since(info.ModTime()) > staleAfter {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("agent status lock timeout")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func readRecord(path string) (record, error) {
	var value record
	err := readJSONFile(path, &value)
	if err == nil {
		err = validateRecord(value)
	}
	return value, err
}

func validateRecord(value record) error {
	if value.Version != 1 {
		return fmt.Errorf("unsupported agent status record version %d", value.Version)
	}
	if err := validatePaneIdentity(value.Pane); err != nil {
		return err
	}
	if _, err := ParseProvider(string(value.Provider)); err != nil {
		return err
	}
	if value.ProviderSessionID == "" || value.Sequence == 0 || value.UpdatedAt.IsZero() {
		return errors.New("incomplete agent status record")
	}
	switch value.State {
	case StateAttention, StateWorking, StateCompleted, StateWaiting, StateUnknown:
	default:
		return fmt.Errorf("unsupported agent status state %q", value.State)
	}
	return nil
}

func validateTrace(value TraceEntry) error {
	if _, err := ParseProvider(string(value.Provider)); err != nil {
		return err
	}
	if err := validatePaneIdentity(value.Pane); err != nil {
		return err
	}
	if value.ObservedAt.IsZero() {
		return errors.New("trace timestamp is missing")
	}
	for _, field := range []struct {
		name        string
		value       string
		max         int
		allowSpaces bool
	}{
		{name: "provider session", value: value.ProviderSessionID, max: 256},
		{name: "turn", value: value.TurnID, max: 256},
		{name: "correlation", value: value.CorrelationID, max: 512},
		{name: "raw event", value: value.RawEvent, max: 80, allowSpaces: true},
		{name: "reason", value: value.Reason, max: 160, allowSpaces: true},
		{name: "error class", value: value.ErrorClass, max: 80, allowSpaces: true},
	} {
		if _, err := safeIdentifier(field.value, field.max, field.allowSpaces); err != nil {
			return fmt.Errorf("invalid trace %s: %w", field.name, err)
		}
	}
	if value.Accepted {
		if value.ProviderSessionID == "" || value.RawEvent == "" || value.Kind == "" || value.ErrorClass != "" {
			return errors.New("incomplete accepted trace")
		}
		switch value.Kind {
		case EventSessionStart, EventTurnStart, EventProgress, EventAttentionCandidate, EventAttentionConfirmed,
			EventAttentionResolved, EventTurnStop, EventSessionEnd, EventSubagentStart, EventSubagentStop, EventFailure:
		default:
			return fmt.Errorf("unsupported accepted trace kind %q", value.Kind)
		}
	} else if value.ErrorClass == "" {
		return errors.New("rejected trace is missing an error class")
	}
	return nil
}

func readJSONFile(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || info.Size() > maxStateFile {
		return errors.New("unsafe state file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, maxStateFile))
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("state file has trailing JSON")
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > maxStateFile {
		return errors.New("state record exceeds 1 MiB")
	}
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("refusing unsafe state file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-agent-status-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	remove := true
	defer func() {
		_ = tmp.Close()
		if remove {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	remove = false
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func samePane(a, b PaneIdentity) bool {
	return a.ServerID == b.ServerID && a.PaneID == b.PaneID && a.PanePID == b.PanePID && a.ProviderPID == b.ProviderPID && a.TmuxSessionID == b.TmuxSessionID
}
func sameLivePane(stored, live PaneIdentity) bool {
	return stored.ServerID == live.ServerID && stored.PaneID == live.PaneID && stored.PanePID == live.PanePID &&
		(live.ProviderPID == 0 || stored.ProviderPID == live.ProviderPID) && stored.TmuxSessionID == live.TmuxSessionID
}
func validateEvent(event Event) error {
	if err := validatePaneIdentity(event.Pane); err != nil {
		return err
	}
	if _, err := ParseProvider(string(event.Provider)); err != nil {
		return err
	}
	if event.ProviderSessionID == "" || event.RawEvent == "" || event.ObservedAt.IsZero() {
		return errors.New("incomplete agent status event")
	}
	return nil
}
