package agentstatus

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestFirstChildEventPersistsValidParent(t *testing.T) {
	for _, kind := range []EventKind{EventSubagentStart, EventSubagentStop, EventProgress} {
		t.Run(string(kind), func(t *testing.T) {
			store := newCoreTestStore(t, testPolicy())
			event := coreEvent(kind, "turn-1", time.Now())
			event.ChildID = "child-1"
			applyCore(t, store, event)
			if err := store.ValidateReadOnly(context.Background()); err != nil {
				t.Fatal(err)
			}
			event.ObservedAt = event.ObservedAt.Add(time.Second)
			event.ChildID = ""
			event.Kind = EventTurnStart
			applyCore(t, store, event)
		})
	}
}

func TestLegacyChildOnlyRecordReadsAsUnknownWithoutRewriting(t *testing.T) {
	store := newCoreTestStore(t, testPolicy())
	event := coreEvent(EventSubagentStart, "turn-1", time.Now())
	event.ChildID = "child-1"
	applyCore(t, store, event)
	path := store.recordPath(recordKey(event.Pane, event.Provider))
	current, err := readRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	current.State = ""
	if err := writeJSONAtomic(path, current); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	restored, err := readRecord(path)
	if err != nil || restored.State != StateUnknown {
		t.Fatalf("state=%q err=%v", restored.State, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("read rewrote record")
	}
}
