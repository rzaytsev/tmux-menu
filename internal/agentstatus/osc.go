package agentstatus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tmux-menu/internal/programstatus"
)

// OSCSnapshot is separate from hook leases: OSC has event-based lifetimes and
// must not expire merely because an agent has been quiet for two minutes.
type OSCSnapshot struct {
	Version       int                    `json:"version"`
	Pane          PaneIdentity           `json:"pane"`
	Provider      Provider               `json:"provider"`
	WrapperPID    int                    `json:"wrapper_pid"`
	ChildPID      int                    `json:"child_pid"`
	UpdatedAt     time.Time              `json:"updated_at"`
	LastWorkingAt time.Time              `json:"last_working_at,omitempty"`
	Records       []programstatus.Record `json:"records"`
}

func (s *Store) oscPath(pane PaneIdentity) string {
	key := fmt.Sprintf("%s\x00%s\x00%d", pane.ServerID, pane.PaneID, pane.PanePID)
	return filepath.Join(s.root, "osc7501", hashKey(key)+".json")
}

func (s *Store) WriteOSC(value OSCSnapshot) error {
	if err := validateOSC(value); err != nil {
		return err
	}
	if err := s.ensureRoot(); err != nil {
		return err
	}
	path := s.oscPath(value.Pane)
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	return writeJSONAtomic(path, value)
}

func (s *Store) ReadOSC(pane PaneIdentity, consume func(int) bool) (OSCSnapshot, error) {
	path := s.oscPath(pane)
	if err := verifyRootIfPresent(s.root); err != nil {
		return OSCSnapshot{}, err
	}
	if err := verifyRootIfPresent(filepath.Dir(path)); err != nil {
		return OSCSnapshot{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return OSCSnapshot{}, err
	}
	if info.Size() > maxStateFile || consume != nil && !consume(int(info.Size())) {
		return OSCSnapshot{}, ErrSnapshotBudget
	}
	var value OSCSnapshot
	if err := readJSONFile(path, &value); err != nil {
		return OSCSnapshot{}, err
	}
	if err := validateOSC(value); err != nil {
		return OSCSnapshot{}, err
	}
	if value.Pane.ServerID != pane.ServerID || value.Pane.PaneID != pane.PaneID || value.Pane.PanePID != pane.PanePID || value.Pane.TmuxSessionID != pane.TmuxSessionID {
		return OSCSnapshot{}, errors.New("OSC pane identity mismatch")
	}
	return value, nil
}

func validateOSC(value OSCSnapshot) error {
	if value.Version != 1 || value.WrapperPID <= 0 || value.ChildPID <= 0 || value.Pane.ServerID == "" || value.Pane.PaneID == "" || value.Pane.PanePID <= 0 || value.UpdatedAt.IsZero() || len(value.Records) > 64 {
		return errors.New("invalid OSC snapshot")
	}
	if _, err := ParseProvider(string(value.Provider)); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range value.Records {
		if seen[r.ID] || r.UpdatedAt.IsZero() {
			return errors.New("invalid OSC records")
		}
		seen[r.ID] = true
		body := "state=" + r.State + ":id=" + r.ID + ":app=" + r.App + ":kind=" + r.Kind
		if r.Progress != nil {
			body += fmt.Sprintf(":progress=%d", *r.Progress)
		}
		if _, ok := programstatus.Parse(body); !ok || r.State == "clear" {
			return errors.New("invalid OSC record")
		}
	}
	return nil
}
