package agentstatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tmux-menu/internal/programstatus"
)

func TestOSCStoreIdentityPrivacyAndBudget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	s, err := NewStore(root, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	value := OSCSnapshot{Version: 1, Pane: PaneIdentity{ServerID: "server", PaneID: "%1", PanePID: 100, TmuxSessionID: "$1"}, Provider: ProviderClaude, WrapperPID: 101, ChildPID: 102, UpdatedAt: now, Records: []programstatus.Record{{State: "working", UpdatedAt: now}}}
	if err := s.WriteOSC(value); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadOSC(value.Pane, nil)
	if err != nil || len(got.Records) != 1 {
		t.Fatalf("read: %+v %v", got, err)
	}
	if _, err := s.ReadOSC(value.Pane, func(int) bool { return false }); err != ErrSnapshotBudget {
		t.Fatalf("budget: %v", err)
	}
	wrong := value.Pane
	wrong.TmuxSessionID = "$2"
	if _, err := s.ReadOSC(wrong, nil); err == nil {
		t.Fatal("accepted different session")
	}
	contents, _ := os.ReadFile(s.oscPath(value.Pane))
	for _, key := range []string{"\"msg\"", "\"title\"", "prompt", "transcript"} {
		if strings.Contains(string(contents), key) {
			t.Fatalf("privacy: %s", key)
		}
	}
	info, _ := os.Stat(s.oscPath(value.Pane))
	if info.Mode().Perm() != 0600 {
		t.Fatal("state permissions")
	}
	if err := os.Chmod(s.oscPath(value.Pane), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadOSC(value.Pane, nil); err == nil {
		t.Fatal("accepted public state")
	}
}

func TestOSCStoreRejectsSymlink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	s, _ := NewStore(root, DefaultPolicy())
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "osc7501")); err != nil {
		t.Fatal(err)
	}
	_, err := s.ReadOSC(PaneIdentity{ServerID: "server", PaneID: "%1", PanePID: 100}, nil)
	if err == nil {
		t.Fatal("followed symlink")
	}
}
