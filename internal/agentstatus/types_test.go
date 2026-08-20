package agentstatus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerFingerprintIdentifiesServerNotClient(t *testing.T) {
	base := ServerFingerprint("/private/tmp/tmux-501/default,43210,0")
	if base == "" {
		t.Fatal("fingerprint is empty")
	}
	if got := ServerFingerprint(" /private/tmp/tmux-501/default , 43210 , 17 "); got != base {
		t.Fatalf("client index changed server fingerprint: got %q want %q", got, base)
	}
	if got := ServerFingerprint("/private/tmp/tmux-501/default,43211,0"); got == base {
		t.Fatalf("server PID did not change fingerprint: %q", got)
	}
	if got := ServerFingerprint("/private/tmp/tmux-501/other,43210,0"); got == base {
		t.Fatalf("server socket did not change fingerprint: %q", got)
	}
	if got := ServerFingerprint("  "); got != "" {
		t.Fatalf("blank TMUX fingerprint = %q, want empty", got)
	}
}

func TestDefaultStateDirHonorsExplicitAndXDGRoots(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit")
	t.Setenv("TMUX_MENU_AGENT_STATE_DIR", explicit)
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "xdg"))
	got, err := DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("explicit state dir = %q, want %q", got, want)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("DefaultStateDir created storage: stat err = %v", err)
	}

	t.Setenv("TMUX_MENU_AGENT_STATE_DIR", "")
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_STATE_HOME", xdg)
	got, err = DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(xdg, "tmux-menu", "agent-status", "v1")
	if got != want {
		t.Fatalf("XDG state dir = %q, want %q", got, want)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("DefaultStateDir created XDG storage: stat err = %v", err)
	}
}
