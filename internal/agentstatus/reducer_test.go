package agentstatus

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReduceTimingAnchorsPreserveTurnAndOnlyAdvanceOnTheirClock(t *testing.T) {
	policy := testPolicy()
	startAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	current, decision := reduce(record{}, coreEvent(EventTurnStart, "turn-1", startAt), policy)
	if !decision.Applied || !current.TurnStartedAt.Equal(startAt) || !current.StateChangedAt.Equal(startAt) || !current.LastEventAt.Equal(startAt) {
		t.Fatalf("turn start timing = %+v decision=%+v", current, decision)
	}

	progressAt := startAt.Add(5 * time.Second)
	current, decision = reduce(current, coreEvent(EventProgress, "turn-1", progressAt), policy)
	if !decision.Applied || !current.TurnStartedAt.Equal(startAt) || !current.StateChangedAt.Equal(startAt) || !current.LastEventAt.Equal(progressAt) {
		t.Fatalf("same-state progress changed the wrong clock: %+v decision=%+v", current, decision)
	}

	attentionAt := startAt.Add(10 * time.Second)
	attention := coreEvent(EventAttentionConfirmed, "turn-1", attentionAt)
	attention.CorrelationID = "id:permission"
	current, decision = reduce(current, attention, policy)
	if !decision.Applied || !current.TurnStartedAt.Equal(startAt) || !current.StateChangedAt.Equal(attentionAt) || !current.LastEventAt.Equal(attentionAt) {
		t.Fatalf("state transition timing = %+v decision=%+v", current, decision)
	}

	renewedAt := startAt.Add(12 * time.Second)
	attention.ObservedAt = renewedAt
	current, decision = reduce(current, attention, policy)
	if !decision.Applied || !current.StateChangedAt.Equal(attentionAt) || !current.LastEventAt.Equal(renewedAt) {
		t.Fatalf("same-state attention changed state clock: %+v decision=%+v", current, decision)
	}

	annotation := resolve(current, renewedAt, policy)
	if !annotation.TurnStartedAt.Equal(startAt) || !annotation.StateChangedAt.Equal(attentionAt) || !annotation.LastEventAt.Equal(renewedAt) {
		t.Fatalf("annotation timing = %+v", annotation)
	}
}

func TestReduceNewTurnAdvancesTurnClockWithoutInventingStateChange(t *testing.T) {
	policy := testPolicy()
	startAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", startAt), policy)
	secondAt := startAt.Add(time.Minute)
	current, decision := reduce(current, coreEvent(EventTurnStart, "turn-2", secondAt), policy)
	if !decision.Applied || !current.TurnStartedAt.Equal(secondAt) || !current.StateChangedAt.Equal(startAt) || !current.LastEventAt.Equal(secondAt) {
		t.Fatalf("new working turn timing = %+v decision=%+v", current, decision)
	}
}

func TestResolveLegacyRecordLeavesTimingUnknown(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	legacy := record{
		Version: 1, Pane: coreTestPane(), Provider: ProviderClaude, ProviderSessionID: "provider-session-1",
		TurnID: "turn-1", State: StateWorking, UpdatedAt: now, WorkingUntil: now.Add(time.Minute),
	}
	annotation := resolve(legacy, now, testPolicy())
	if !annotation.TurnStartedAt.IsZero() || !annotation.StateChangedAt.IsZero() || !annotation.LastEventAt.IsZero() {
		t.Fatalf("legacy timing was invented: %+v", annotation)
	}
}

func TestResolveWinningChildCarriesItsTimingAnchors(t *testing.T) {
	policy := testPolicy()
	startAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventSessionStart, "", startAt), policy)
	childStart := coreEvent(EventSubagentStart, "turn-1", startAt.Add(time.Second))
	childStart.ChildID = "child-1"
	current, _ = reduce(current, childStart, policy)
	childProgress := coreEvent(EventProgress, "turn-1", startAt.Add(2*time.Second))
	childProgress.ChildID = "child-1"
	current, _ = reduce(current, childProgress, policy)
	childAttention := coreEvent(EventAttentionConfirmed, "turn-1", startAt.Add(3*time.Second))
	childAttention.ChildID = "child-1"
	childAttention.CorrelationID = "id:child"
	current, _ = reduce(current, childAttention, policy)

	annotation := resolve(current, childAttention.ObservedAt, policy)
	if annotation.State != StateAttention || !annotation.TurnStartedAt.Equal(childStart.ObservedAt) || !annotation.StateChangedAt.Equal(childAttention.ObservedAt) || !annotation.LastEventAt.Equal(childAttention.ObservedAt) {
		t.Fatalf("winning child timing = %+v", annotation)
	}
	if len(annotation.Children) != 1 || !annotation.Children[0].TurnStartedAt.Equal(childStart.ObservedAt) {
		t.Fatalf("child annotation timing = %+v", annotation.Children)
	}
}

func TestTimingMetadataSerializationContainsNoPayloadFields(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), testPolicy())
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{"turn_started_at", "state_changed_at", "last_event_at"} {
		if !strings.Contains(text, want) {
			t.Fatalf("serialized timing metadata missing %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{"prompt", "transcript", "cwd", "model_output", "tool_input", "tool_result"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("serialized record contains forbidden payload field %q: %s", forbidden, text)
		}
	}
}

func TestReduceStopDominatesSameTurnCallbacks(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, decision := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	if !decision.Applied {
		t.Fatalf("turn start decision = %+v", decision)
	}
	current, decision = reduce(current, coreEvent(EventAttentionConfirmed, "turn-1", now.Add(time.Second)), policy)
	if !decision.Applied || current.State != StateAttention {
		t.Fatalf("attention result = %+v, decision = %+v", current, decision)
	}
	current, decision = reduce(current, coreEvent(EventTurnStop, "turn-1", now.Add(2*time.Second)), policy)
	if !decision.Applied || current.State != StateCompleted {
		t.Fatalf("stop result = %+v, decision = %+v", current, decision)
	}
	stopped := current

	for _, kind := range []EventKind{EventProgress, EventAttentionConfirmed, EventAttentionResolved, EventSubagentStart, EventSubagentStop, EventFailure} {
		late := coreEvent(kind, "turn-1", now.Add(3*time.Second))
		late.CorrelationID = "call-1"
		if kind == EventSubagentStart || kind == EventSubagentStop {
			late.ChildID = "late-child"
		}
		got, gotDecision := reduce(stopped, late, policy)
		if gotDecision.Applied || gotDecision.Reason != "closed-turn" {
			t.Errorf("late %q decision = %+v, want closed-turn rejection", kind, gotDecision)
		}
		if !reflect.DeepEqual(got, stopped) {
			t.Errorf("late %q mutated completed record:\n got  %+v\n want %+v", kind, got, stopped)
		}
	}
	for _, kind := range []EventKind{EventProgress, EventAttentionConfirmed, EventAttentionResolved, EventTurnStop, EventFailure} {
		lateChild := coreEvent(kind, "turn-1", now.Add(3*time.Second))
		lateChild.ChildID = "unknown-late-child"
		lateChild.CorrelationID = "call-1"
		got, gotDecision := reduce(stopped, lateChild, policy)
		if gotDecision.Applied || gotDecision.Reason != "closed-turn" {
			t.Errorf("late child %q decision = %+v, want closed-turn rejection", kind, gotDecision)
		}
		if !reflect.DeepEqual(got, stopped) {
			t.Errorf("late child %q mutated completed record:\n got  %+v\n want %+v", kind, got, stopped)
		}
	}

	next, decision := reduce(stopped, coreEvent(EventTurnStart, "turn-2", now.Add(4*time.Second)), policy)
	if !decision.Applied || next.State != StateWorking || next.TurnID != "turn-2" {
		t.Fatalf("new turn did not supersede closed turn: record = %+v decision = %+v", next, decision)
	}
	lateStop, decision := reduce(next, coreEvent(EventTurnStop, "turn-1", now.Add(5*time.Second)), policy)
	if decision.Applied || decision.Reason != "closed-turn" || !reflect.DeepEqual(lateStop, next) {
		t.Fatalf("old stop crossed new-turn fence: record = %+v decision = %+v", lateStop, decision)
	}
}

func TestReduceDuplicateClosedTurnStartIsRejectedButDistinctTurnStarts(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	current, _ = reduce(current, coreEvent(EventTurnStop, "turn-1", now.Add(time.Second)), policy)
	stopped := current

	duplicate, decision := reduce(stopped, coreEvent(EventTurnStart, "turn-1", now.Add(2*time.Second)), policy)
	if decision.Applied || decision.Reason != "closed-turn" || !reflect.DeepEqual(duplicate, stopped) {
		t.Fatalf("duplicate closed TurnStart reopened completed turn: record=%+v decision=%+v", duplicate, decision)
	}

	next, decision := reduce(stopped, coreEvent(EventTurnStart, "turn-2", now.Add(3*time.Second)), policy)
	if !decision.Applied || next.State != StateWorking || next.TurnID != "turn-2" {
		t.Fatalf("distinct TurnStart did not open new turn: record=%+v decision=%+v", next, decision)
	}
}

func TestReduceSessionEndFencesLateCallbacksUntilExplicitRestart(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	end := coreEvent(EventSessionEnd, "", now.Add(time.Second))
	end.RawEvent = "SessionEnd"
	current, decision := reduce(current, end, policy)
	if !decision.Applied || resolve(current, now.Add(time.Second), policy).State != StateUnknown {
		t.Fatalf("SessionEnd result=%+v decision=%+v", current, decision)
	}
	ended := current

	for _, kind := range []EventKind{EventProgress, EventAttentionConfirmed, EventAttentionResolved, EventTurnStop, EventFailure, EventObservedOnly} {
		late := coreEvent(kind, "turn-1", now.Add(2*time.Second))
		late.CorrelationID = "id:late"
		got, gotDecision := reduce(ended, late, policy)
		if gotDecision.Applied || !reflect.DeepEqual(got, ended) {
			t.Errorf("late event %q reopened ended session: record=%+v decision=%+v", kind, got, gotDecision)
		}
	}

	restart := coreEvent(EventSessionStart, "", now.Add(3*time.Second))
	restart.RawEvent = "SessionStart"
	restarted, decision := reduce(ended, restart, policy)
	if !decision.Applied || restarted.State != StateWaiting {
		t.Fatalf("same provider session did not restart explicitly: record=%+v decision=%+v", restarted, decision)
	}

	newSession := coreEvent(EventSessionStart, "", now.Add(4*time.Second))
	newSession.ProviderSessionID = "provider-session-2"
	newSession.RawEvent = "SessionStart"
	replaced, decision := reduce(ended, newSession, policy)
	if !decision.Applied || replaced.ProviderSessionID != "provider-session-2" || replaced.State != StateWaiting {
		t.Fatalf("new provider session did not replace ended session: record=%+v decision=%+v", replaced, decision)
	}
}

func TestReduceCompactSessionStartPreservesActiveTurnEvidence(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	for _, provider := range []Provider{ProviderCodex, ProviderClaude} {
		t.Run(string(provider), func(t *testing.T) {
			first := coreEvent(EventTurnStart, "turn-1", now)
			first.Provider = provider
			current, _ := reduce(record{}, first, policy)
			current, _ = reduce(current, func() Event {
				stop := coreEvent(EventTurnStop, "turn-1", now.Add(time.Second))
				stop.Provider = provider
				return stop
			}(), policy)
			second := coreEvent(EventTurnStart, "turn-2", now.Add(2*time.Second))
			second.Provider = provider
			current, _ = reduce(current, second, policy)
			child := coreEvent(EventSubagentStart, "turn-2", now.Add(3*time.Second))
			child.Provider = provider
			child.ChildID = "child-1"
			current, _ = reduce(current, child, policy)
			attention := coreEvent(EventAttentionConfirmed, "turn-2", now.Add(4*time.Second))
			attention.Provider = provider
			attention.CorrelationID = "id:permission"
			current, _ = reduce(current, attention, policy)
			before := current

			compact := coreEvent(EventSessionStart, "", now.Add(5*time.Second))
			compact.Provider = provider
			compact.RawEvent = "SessionStart"
			compact.Source = "compact"
			got, decision := reduce(current, compact, policy)
			if decision.Applied || decision.Reason != "compact-session-preserved" {
				t.Fatalf("compact SessionStart decision = %+v", decision)
			}
			if !reflect.DeepEqual(got, before) {
				t.Fatalf("compact SessionStart changed active evidence:\n got  %+v\n want %+v", got, before)
			}

			ordinary := compact
			ordinary.Source = "resume"
			reset, decision := reduce(current, ordinary, policy)
			if !decision.Applied || reset.State != StateWaiting || reset.TurnID != "" || len(reset.Children) != 0 || len(reset.ClosedTurnIDs) != 0 || reset.AttentionCorrelation != "" {
				t.Fatalf("ordinary SessionStart did not reset incarnation state: record=%+v decision=%+v", reset, decision)
			}
		})
	}
}

func TestReduceParentStopPreservesExistingBackgroundChild(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	childStart := coreEvent(EventSubagentStart, "turn-1", now.Add(time.Second))
	childStart.ChildID = "child-1"
	childStart.ChildType = "reviewer"
	current, decision := reduce(current, childStart, policy)
	if !decision.Applied {
		t.Fatalf("child start decision = %+v", decision)
	}
	current, decision = reduce(current, coreEvent(EventTurnStop, "turn-1", now.Add(2*time.Second)), policy)
	if !decision.Applied {
		t.Fatalf("stop decision = %+v", decision)
	}
	annotation := resolve(current, now.Add(3*time.Second), policy)
	if annotation.State != StateWorking || len(annotation.Children) != 1 || annotation.Children[0].ID != "child-1" || annotation.Children[0].State != StateWorking {
		t.Fatalf("parent Stop erased still-running background child: annotation = %+v", annotation)
	}

	childProgress := coreEvent(EventProgress, "turn-1", now.Add(4*time.Second))
	childProgress.ChildID = "child-1"
	current, decision = reduce(current, childProgress, policy)
	if !decision.Applied || resolve(current, now.Add(4*time.Second), policy).State != StateWorking {
		t.Fatalf("existing background child could not renew after parent Stop: record=%+v decision=%+v", current, decision)
	}
	childStop := coreEvent(EventSubagentStop, "turn-1", now.Add(5*time.Second))
	childStop.ChildID = "child-1"
	current, decision = reduce(current, childStop, policy)
	annotation = resolve(current, now.Add(5*time.Second), policy)
	if !decision.Applied || annotation.State != StateCompleted || len(annotation.Children) != 1 || annotation.Children[0].State != StateCompleted {
		t.Fatalf("existing background child could not finish after parent Stop: annotation=%+v decision=%+v", annotation, decision)
	}
}

func TestReduceAttentionRequiresCorrelatedResolution(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	attention := coreEvent(EventAttentionConfirmed, "turn-1", now.Add(time.Second))
	attention.CorrelationID = "id:expected"
	current, decision := reduce(current, attention, policy)
	if !decision.Applied || current.State != StateAttention {
		t.Fatalf("attention result = %+v decision = %+v", current, decision)
	}
	waiting := current

	progress := coreEvent(EventProgress, "turn-1", now.Add(2*time.Second))
	progress.CorrelationID = "id:unrelated"
	got, decision := reduce(waiting, progress, policy)
	if decision.Applied || decision.Reason != "uncorrelated-progress" || !reflect.DeepEqual(got, waiting) {
		t.Fatalf("unrelated progress cleared attention: record = %+v decision = %+v", got, decision)
	}
	parallelTool := progress
	parallelTool.RawEvent = "PreToolUse"
	got, decision = reduce(waiting, parallelTool, policy)
	if decision.Applied || !reflect.DeepEqual(got, waiting) {
		t.Fatalf("unrelated parallel PreToolUse cleared attention: record = %+v decision = %+v", got, decision)
	}

	wrong := coreEvent(EventAttentionResolved, "turn-1", now.Add(3*time.Second))
	wrong.CorrelationID = "id:other"
	got, decision = reduce(waiting, wrong, policy)
	if decision.Applied || decision.Reason != "attention-correlation-mismatch" || !reflect.DeepEqual(got, waiting) {
		t.Fatalf("wrong correlation cleared attention: record = %+v decision = %+v", got, decision)
	}

	matched := coreEvent(EventAttentionResolved, "turn-1", now.Add(4*time.Second))
	matched.CorrelationID = "id:expected"
	got, decision = reduce(waiting, matched, policy)
	if !decision.Applied || got.State != StateWorking || got.AttentionCorrelation != "" {
		t.Fatalf("matching resolution result = %+v decision = %+v", got, decision)
	}
}

func TestReduceFailureAndDenialResolveOnlyCorrelatedAttention(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	attention := coreEvent(EventAttentionConfirmed, "turn-1", now.Add(time.Second))
	attention.CorrelationID = "id:permission"
	current, _ = reduce(current, attention, policy)

	for _, raw := range []string{"PostToolUseFailure", "PermissionDenied"} {
		failure := coreEvent(EventFailure, "turn-1", now.Add(2*time.Second))
		failure.RawEvent = raw
		failure.Reason = "failure"
		failure.CorrelationID = "id:other"
		got, decision := reduce(current, failure, policy)
		if decision.Applied || decision.Reason != "failure-observed" {
			t.Errorf("unrelated %s decision = %+v, want visible-only failure", raw, decision)
		}
		if !reflect.DeepEqual(got, current) || got.State != StateAttention {
			t.Errorf("unrelated %s cleared attention: got %+v want %+v", raw, got, current)
		}

		failure.CorrelationID = "id:permission"
		got, decision = reduce(current, failure, policy)
		if !decision.Applied || got.State != StateUnknown || got.AttentionCorrelation != "" {
			t.Errorf("correlated %s did not resolve to non-authoritative unknown: got %+v decision=%+v", raw, got, decision)
		}
	}
}

func TestReduceStopFailureEndsMatchingTurnAsNonAuthoritativeUnknown(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	for _, initial := range []State{StateWorking, StateAttention} {
		t.Run(string(initial), func(t *testing.T) {
			current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
			if initial == StateAttention {
				attention := coreEvent(EventAttentionConfirmed, "turn-1", now.Add(time.Second))
				attention.CorrelationID = "id:permission"
				current, _ = reduce(current, attention, policy)
			}
			failure := coreEvent(EventFailure, "turn-1", now.Add(2*time.Second))
			failure.RawEvent = "StopFailure"
			failure.Reason = "turn-failed"
			failure.CorrelationID = "id:unrelated"
			got, decision := reduce(current, failure, policy)
			if !decision.Applied || got.State != StateUnknown || got.TurnID != "turn-1" || got.RawEvent != "StopFailure" {
				t.Fatalf("StopFailure from %s = %+v decision=%+v", initial, got, decision)
			}
			annotation := resolve(got, failure.ObservedAt, policy)
			if annotation.State != StateUnknown || annotation.Fresh {
				t.Fatalf("StopFailure annotation from %s = %+v", initial, annotation)
			}
		})

		t.Run(string(initial)+"-unrelated-tool", func(t *testing.T) {
			current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
			if initial == StateAttention {
				attention := coreEvent(EventAttentionConfirmed, "turn-1", now.Add(time.Second))
				attention.CorrelationID = "id:permission"
				current, _ = reduce(current, attention, policy)
			}
			toolFailure := coreEvent(EventFailure, "turn-1", now.Add(2*time.Second))
			toolFailure.RawEvent = "PostToolUseFailure"
			toolFailure.Reason = "tool-failed"
			toolFailure.CorrelationID = "id:other"
			got, decision := reduce(current, toolFailure, policy)
			if decision.Applied || !reflect.DeepEqual(got, current) {
				t.Fatalf("unrelated tool failure cleared %s: record=%+v decision=%+v", initial, got, decision)
			}
		})
	}
}

func TestWorkingLeaseLearnsCadenceWithinBounds(t *testing.T) {
	policy := Policy{WorkingTTL: 10 * time.Second, ChildRetention: time.Minute, LockTimeout: time.Second}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	if got := current.WorkingUntil.Sub(now); got != policy.WorkingTTL {
		t.Fatalf("initial lease = %s, want floor %s", got, policy.WorkingTTL)
	}

	progressAt := now.Add(4 * time.Second)
	current, _ = reduce(current, coreEvent(EventProgress, "turn-1", progressAt), policy)
	if got := current.WorkingUntil.Sub(progressAt); got != 12*time.Second {
		t.Fatalf("cadence lease = %s, want 12s", got)
	}

	veryLate := now.Add(time.Hour)
	current, _ = reduce(current, coreEvent(EventProgress, "turn-1", veryLate), policy)
	if got := current.WorkingUntil.Sub(veryLate); got != 10*time.Minute {
		t.Fatalf("bounded lease = %s, want 10m cap", got)
	}
}

func TestWorkingLeaseDoesNotLearnIdleGapsAcrossBoundaries(t *testing.T) {
	policy := Policy{WorkingTTL: 10 * time.Second, ChildRetention: time.Minute, LockTimeout: time.Second}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

	t.Run("turn", func(t *testing.T) {
		current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
		current, _ = reduce(current, coreEvent(EventProgress, "turn-1", now.Add(4*time.Second)), policy)
		current, _ = reduce(current, coreEvent(EventTurnStop, "turn-1", now.Add(5*time.Second)), policy)
		startAt := now.Add(time.Hour)
		current, decision := reduce(current, coreEvent(EventTurnStart, "turn-2", startAt), policy)
		if !decision.Applied || current.WorkingUntil.Sub(startAt) != policy.WorkingTTL || current.ObservedGap != 0 || !current.LastProgressAt.Equal(startAt) {
			t.Fatalf("new turn inherited prior cadence: record=%+v decision=%+v", current, decision)
		}
	})

	t.Run("session", func(t *testing.T) {
		current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
		current, _ = reduce(current, coreEvent(EventProgress, "turn-1", now.Add(4*time.Second)), policy)
		end := coreEvent(EventSessionEnd, "", now.Add(5*time.Second))
		end.RawEvent = "SessionEnd"
		current, _ = reduce(current, end, policy)
		start := coreEvent(EventSessionStart, "", now.Add(time.Hour))
		start.RawEvent = "SessionStart"
		current, _ = reduce(current, start, policy)
		startAt := now.Add(2 * time.Hour)
		current, decision := reduce(current, coreEvent(EventTurnStart, "turn-2", startAt), policy)
		if !decision.Applied || current.WorkingUntil.Sub(startAt) != policy.WorkingTTL || current.ObservedGap != 0 || !current.LastProgressAt.Equal(startAt) {
			t.Fatalf("new session inherited prior cadence: record=%+v decision=%+v", current, decision)
		}
	})
}

func TestWorkingLeaseDoesNotLearnHumanAttentionWait(t *testing.T) {
	policy := Policy{WorkingTTL: 10 * time.Second, ChildRetention: time.Minute, LockTimeout: time.Second}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	current, _ = reduce(current, coreEvent(EventProgress, "turn-1", now.Add(4*time.Second)), policy)
	attention := coreEvent(EventAttentionConfirmed, "turn-1", now.Add(5*time.Second))
	attention.CorrelationID = "id:permission"
	current, _ = reduce(current, attention, policy)
	resolvedAt := now.Add(time.Hour)
	resolved := coreEvent(EventAttentionResolved, "turn-1", resolvedAt)
	resolved.CorrelationID = attention.CorrelationID
	current, decision := reduce(current, resolved, policy)
	if !decision.Applied || current.WorkingUntil.Sub(resolvedAt) != 12*time.Second {
		t.Fatalf("attention wait inflated working lease: record=%+v decision=%+v", current, decision)
	}
}

func TestReducePrunesExpiredChildrenAndPreservesCompletionCAS(t *testing.T) {
	policy := Policy{WorkingTTL: time.Minute, ChildRetention: 10 * time.Minute, LockTimeout: time.Second}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current := record{
		Version:           1,
		Pane:              coreTestPane(),
		Provider:          ProviderClaude,
		ProviderSessionID: "provider-session-1",
		TurnID:            "turn-1",
		State:             StateWorking,
		UpdatedAt:         now.Add(-time.Minute),
		WorkingUntil:      now.Add(time.Minute),
		Children: map[string]childRecord{
			"expired-attention": {ID: "expired-attention", State: StateAttention, UpdatedAt: now.Add(-policy.ChildRetention - time.Nanosecond)},
			"completed":         {ID: "completed", State: StateCompleted, UpdatedAt: now.Add(-time.Second)},
		},
	}
	stop := coreEvent(EventTurnStop, "turn-1", now)
	got, decision := reduce(current, stop, policy)
	if !decision.Applied || len(got.Children) != 1 {
		t.Fatalf("expired children were not pruned: record=%+v decision=%+v", got, decision)
	}
	if _, ok := got.Children["completed"]; !ok {
		t.Fatalf("fresh completion was pruned: %+v", got.Children)
	}
	annotation := resolve(got, now, policy)
	if annotation.State != StateCompleted || annotation.AcknowledgeToken == "" || len(annotation.Children) != 1 {
		t.Fatalf("pruning broke completion rollup/CAS: %+v", annotation)
	}
}

func TestReduceRetainsDeterministicBoundedChildren(t *testing.T) {
	policy := Policy{WorkingTTL: time.Minute, ChildRetention: time.Hour, LockTimeout: time.Second}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	children := make(map[string]childRecord, maxRetainedChildren+3)
	for i := 0; i < maxRetainedChildren+1; i++ {
		id := fmt.Sprintf("child-%03d", i)
		children[id] = childRecord{ID: id, State: StateUnknown, UpdatedAt: now.Add(-time.Minute)}
	}
	children["important-attention"] = childRecord{ID: "important-attention", State: StateAttention, UpdatedAt: now.Add(-time.Minute)}
	children["important-completed"] = childRecord{ID: "important-completed", State: StateCompleted, UpdatedAt: now.Add(-time.Minute)}
	current := record{
		Version:           1,
		Pane:              coreTestPane(),
		Provider:          ProviderClaude,
		ProviderSessionID: "provider-session-1",
		TurnID:            "turn-1",
		State:             StateWaiting,
		UpdatedAt:         now.Add(-time.Minute),
		WorkingUntil:      now.Add(time.Minute),
		Children:          children,
	}
	got, decision := reduce(current, coreEvent(EventProgress, "turn-1", now), policy)
	if !decision.Applied || len(got.Children) != maxRetainedChildren {
		t.Fatalf("bounded children count = %d, decision=%+v", len(got.Children), decision)
	}
	for _, id := range []string{"important-attention", "important-completed", fmt.Sprintf("child-%03d", maxRetainedChildren-3)} {
		if _, ok := got.Children[id]; !ok {
			t.Errorf("priority/deterministic child %q was not retained", id)
		}
	}
	if _, ok := got.Children[fmt.Sprintf("child-%03d", maxRetainedChildren-2)]; ok {
		t.Errorf("lexically later low-priority child was retained past deterministic cap")
	}
	annotation := resolve(got, now, policy)
	if annotation.State != StateAttention || len(annotation.Children) != maxRetainedChildren {
		t.Fatalf("bounded children broke rollup: %+v", annotation)
	}
}

func TestResolveExpiresWorkingToFallback(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	stored := current
	before := resolve(current, current.WorkingUntil.Add(-time.Nanosecond), policy)
	if before.State != StateWorking || !before.Fresh {
		t.Fatalf("fresh working annotation = %+v", before)
	}
	expired := resolve(current, current.WorkingUntil, policy)
	if expired.State != StateUnknown || expired.Fresh {
		t.Fatalf("expired working annotation = %+v, want stale unknown", expired)
	}
	again := resolve(current, current.WorkingUntil.Add(time.Minute), policy)
	if !reflect.DeepEqual(current, stored) || !again.TurnStartedAt.Equal(expired.TurnStartedAt) || !again.StateChangedAt.Equal(expired.StateChangedAt) || !again.LastEventAt.Equal(expired.LastEventAt) {
		t.Fatalf("polling mutated or renewed timing: record=%+v stored=%+v first=%+v again=%+v", current, stored, expired, again)
	}
}

func TestResolveRollsUpFreshChildrenBySeverityAndRetention(t *testing.T) {
	policy := Policy{WorkingTTL: time.Minute, ChildRetention: 10 * time.Minute, LockTimeout: time.Second}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current := record{
		Version:           1,
		Pane:              coreTestPane(),
		Provider:          ProviderClaude,
		ProviderSessionID: "provider-session-1",
		State:             StateWaiting,
		UpdatedAt:         now.Add(-time.Minute),
		Children: map[string]childRecord{
			"working": {
				ID:           "working",
				State:        StateWorking,
				UpdatedAt:    now,
				WorkingUntil: now.Add(time.Minute),
			},
			"completed": {ID: "completed", State: StateCompleted, UpdatedAt: now.Add(-time.Second)},
			"old-attention": {
				ID:        "old-attention",
				State:     StateAttention,
				UpdatedAt: now.Add(-policy.ChildRetention - time.Nanosecond),
			},
		},
	}
	annotation := resolve(current, now, policy)
	if annotation.State != StateWorking || len(annotation.Children) != 2 {
		t.Fatalf("child rollup = %+v, want fresh working over completed and old child removed", annotation)
	}
	if annotation.Children[0].ID != "working" || annotation.Children[1].ID != "completed" {
		t.Fatalf("children not severity ordered: %+v", annotation.Children)
	}

	afterLease := resolve(current, now.Add(2*time.Minute), policy)
	if afterLease.State != StateCompleted || len(afterLease.Children) != 2 || afterLease.Children[0].ID != "completed" || afterLease.Children[1].State != StateUnknown {
		t.Fatalf("expired child rollup = %+v", afterLease)
	}

	afterRetention := resolve(current, now.Add(policy.ChildRetention+time.Minute), policy)
	if afterRetention.State != StateUnknown || afterRetention.Fresh || len(afterRetention.Children) != 0 {
		t.Fatalf("retained stale children = %+v", afterRetention)
	}
}

func TestResolveWinningChildCarriesChildEvidenceProvenance(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	parentUpdated := now.Add(-time.Minute)
	childUpdated := now.Add(-time.Second)
	current := record{
		Version:           1,
		Pane:              coreTestPane(),
		Provider:          ProviderClaude,
		ProviderSessionID: "provider-session-1",
		State:             StateWaiting,
		Reason:            "parent-waiting",
		RawEvent:          "SessionStart",
		UpdatedAt:         parentUpdated,
		WorkingUntil:      now.Add(time.Minute),
		Children: map[string]childRecord{
			"child-attention": {
				ID:        "child-attention",
				State:     StateAttention,
				Reason:    "child-permission",
				RawEvent:  "PermissionRequest",
				UpdatedAt: childUpdated,
			},
		},
	}
	annotation := resolve(current, now, policy)
	if annotation.State != StateAttention || annotation.Reason != "child-permission" || annotation.RawEvent != "PermissionRequest" || !annotation.UpdatedAt.Equal(childUpdated) {
		t.Fatalf("winning child state carried parent evidence: %+v", annotation)
	}
}

func TestProviderSessionReplacementResetsIncarnation(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)
	child := coreEvent(EventSubagentStart, "turn-1", now.Add(time.Second))
	child.ChildID = "child-1"
	current, _ = reduce(current, child, policy)
	current, _ = reduce(current, coreEvent(EventTurnStop, "turn-1", now.Add(2*time.Second)), policy)

	start := coreEvent(EventSessionStart, "", now.Add(3*time.Second))
	start.ProviderSessionID = "provider-session-2"
	got, decision := reduce(current, start, policy)
	if !decision.Applied || got.ProviderSessionID != "provider-session-2" || got.State != StateWaiting || got.TurnID != "" {
		t.Fatalf("replacement result = %+v decision = %+v", got, decision)
	}
	if len(got.Children) != 0 || len(got.ClosedTurnIDs) != 0 || got.AttentionCorrelation != "" {
		t.Fatalf("replacement retained prior incarnation state: %+v", got)
	}
}

func TestDelayedRetiredProviderSessionCannotReplaceCurrentIncarnation(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	startA := coreEvent(EventSessionStart, "", now)
	startA.ProviderSessionID = "provider-session-a"
	current, _ := reduce(record{}, startA, policy)
	startB := coreEvent(EventTurnStart, "turn-b", now.Add(time.Second))
	startB.ProviderSessionID = "provider-session-b"
	current, decision := reduce(current, startB, policy)
	if !decision.Applied || current.ProviderSessionID != "provider-session-b" || current.State != StateWorking {
		t.Fatalf("current incarnation = %+v decision=%+v", current, decision)
	}

	for _, kind := range []EventKind{EventProgress, EventAttentionConfirmed, EventTurnStop, EventSessionEnd, EventObservedOnly, EventFailure} {
		delayed := coreEvent(kind, "turn-a", now.Add(2*time.Second))
		delayed.ProviderSessionID = "provider-session-a"
		delayed.CorrelationID = "id:old"
		got, gotDecision := reduce(current, delayed, policy)
		if gotDecision.Applied || !reflect.DeepEqual(got, current) {
			t.Errorf("retired session event %q replaced current incarnation: got=%+v decision=%+v", kind, got, gotDecision)
		}
	}
}

func TestProviderSessionReplacementRequiresSessionOrTurnStart(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	current, _ := reduce(record{}, coreEvent(EventTurnStart, "turn-1", now), policy)

	for _, kind := range []EventKind{EventProgress, EventAttentionConfirmed, EventAttentionResolved, EventTurnStop, EventSessionEnd, EventObservedOnly, EventFailure} {
		foreign := coreEvent(kind, "turn-foreign", now.Add(time.Second))
		foreign.ProviderSessionID = "provider-session-3"
		foreign.CorrelationID = "id:foreign"
		got, decision := reduce(current, foreign, policy)
		if decision.Applied || !reflect.DeepEqual(got, current) {
			t.Errorf("third-session %q hijacked current incarnation: record=%+v decision=%+v", kind, got, decision)
		}
	}

	turnStart := coreEvent(EventTurnStart, "turn-2", now.Add(2*time.Second))
	turnStart.ProviderSessionID = "provider-session-2"
	replaced, decision := reduce(current, turnStart, policy)
	if !decision.Applied || replaced.ProviderSessionID != "provider-session-2" || replaced.TurnID != "turn-2" || replaced.State != StateWorking {
		t.Fatalf("authoritative TurnStart did not replace incarnation: record=%+v decision=%+v", replaced, decision)
	}
}

func coreTestPane() PaneIdentity {
	return PaneIdentity{ServerID: "server-core", PaneID: "%41", PanePID: 4100, TmuxSessionID: "$9"}
}

func coreEvent(kind EventKind, turnID string, at time.Time) Event {
	return Event{
		Pane:              coreTestPane(),
		Provider:          ProviderClaude,
		ProviderSessionID: "provider-session-1",
		TurnID:            turnID,
		Kind:              kind,
		RawEvent:          string(kind),
		Reason:            string(kind),
		ObservedAt:        at,
	}
}

func testPolicy() Policy {
	return Policy{WorkingTTL: 30 * time.Second, ChildRetention: 10 * time.Minute, LockTimeout: time.Second}
}
