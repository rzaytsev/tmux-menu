package agentstatus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

var ErrNotAcknowledgeable = errors.New("agent state is not acknowledgeable")

const maxRetainedChildren = 128

type record struct {
	Version              int                    `json:"version"`
	Pane                 PaneIdentity           `json:"pane"`
	Provider             Provider               `json:"provider"`
	ProviderSessionID    string                 `json:"provider_session_id"`
	RetiredSessionIDs    []string               `json:"retired_session_ids,omitempty"`
	SessionClosed        bool                   `json:"session_closed,omitempty"`
	TurnID               string                 `json:"turn_id,omitempty"`
	ClosedTurnIDs        []string               `json:"closed_turn_ids,omitempty"`
	Sequence             uint64                 `json:"sequence"`
	State                State                  `json:"state"`
	Reason               string                 `json:"reason,omitempty"`
	RawEvent             string                 `json:"raw_event,omitempty"`
	UpdatedAt            time.Time              `json:"updated_at"`
	TurnStartedAt        time.Time              `json:"turn_started_at,omitempty"`
	StateChangedAt       time.Time              `json:"state_changed_at,omitempty"`
	LastEventAt          time.Time              `json:"last_event_at,omitempty"`
	WorkingUntil         time.Time              `json:"working_until,omitempty"`
	LastProgressAt       time.Time              `json:"last_progress_at,omitempty"`
	ObservedGap          time.Duration          `json:"observed_gap,omitempty"`
	AttentionCorrelation string                 `json:"attention_correlation,omitempty"`
	Children             map[string]childRecord `json:"children,omitempty"`
}

type childRecord struct {
	ID                   string    `json:"id"`
	Type                 string    `json:"type,omitempty"`
	TurnID               string    `json:"turn_id,omitempty"`
	State                State     `json:"state"`
	Reason               string    `json:"reason,omitempty"`
	RawEvent             string    `json:"raw_event,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
	TurnStartedAt        time.Time `json:"turn_started_at,omitempty"`
	StateChangedAt       time.Time `json:"state_changed_at,omitempty"`
	LastEventAt          time.Time `json:"last_event_at,omitempty"`
	WorkingUntil         time.Time `json:"working_until,omitempty"`
	AttentionCorrelation string    `json:"attention_correlation,omitempty"`
}

func reduce(previous record, event Event, policy Policy) (record, Decision) {
	next := previous
	if next.Version == 0 {
		next.Version = 1
		next.Pane = event.Pane
		next.Provider = event.Provider
	}
	sessionChanged := next.ProviderSessionID != "" && next.ProviderSessionID != event.ProviderSessionID
	if sessionChanged && containsOpaqueID(next.RetiredSessionIDs, event.ProviderSessionID) {
		return previous, Decision{Reason: "retired-provider-session"}
	}
	if sessionChanged && event.Kind != EventSessionStart && event.Kind != EventTurnStart {
		return previous, Decision{Reason: "unestablished-provider-session"}
	}
	if !sessionChanged && compactSessionStartDuringActiveTurn(next, event) {
		return previous, Decision{Reason: "compact-session-preserved"}
	}
	if next.ProviderSessionID != event.ProviderSessionID {
		retired := append([]string(nil), next.RetiredSessionIDs...)
		if next.ProviderSessionID != "" {
			retired = addOpaqueID(retired, next.ProviderSessionID, 16)
		}
		next = record{
			Version:           1,
			Pane:              event.Pane,
			Provider:          event.Provider,
			ProviderSessionID: event.ProviderSessionID,
			RetiredSessionIDs: retired,
			Children:          map[string]childRecord{},
		}
	}
	if next.Children == nil {
		next.Children = map[string]childRecord{}
	}
	next.Children = retainChildren(next.Children, event.ObservedAt, policy)
	if next.SessionClosed && event.Kind != EventSessionStart {
		return previous, Decision{Reason: "closed-provider-session"}
	}
	if event.Kind == EventObservedOnly {
		if !sessionChanged {
			return previous, Decision{Reason: "observed-only"}
		}
		next.Sequence = previous.Sequence + 1
		next.State = StateUnknown
		next.Reason = "provider-session-replaced"
		next.RawEvent = event.RawEvent
		next.UpdatedAt = event.ObservedAt
		next.StateChangedAt = event.ObservedAt
		next.LastEventAt = event.ObservedAt
		return next, Decision{Applied: true, Reason: "provider-session-replaced"}
	}
	if event.ChildID != "" {
		_, knownChild := next.Children[event.ChildID]
		closedTurn := event.TurnID != "" && containsTurn(next.ClosedTurnIDs, event.TurnID)
		foreignTurn := next.TurnID != "" && event.TurnID != "" && event.TurnID != next.TurnID && event.Kind != EventTurnStart && event.Kind != EventSessionStart
		if (closedTurn || foreignTurn) && !knownChild {
			if closedTurn {
				return previous, Decision{Reason: "closed-turn"}
			}
			return previous, Decision{Reason: "foreign-turn"}
		}
		if closedTurn && event.Kind == EventSubagentStart {
			return previous, Decision{Reason: "closed-turn"}
		}
		return reduceChild(previous, next, event, policy, sessionChanged)
	}
	if event.TurnID != "" && containsTurn(next.ClosedTurnIDs, event.TurnID) {
		return previous, Decision{Reason: "closed-turn"}
	}
	if next.TurnID != "" && event.TurnID != "" && event.TurnID != next.TurnID && event.Kind != EventTurnStart && event.Kind != EventSessionStart {
		return previous, Decision{Reason: "foreign-turn"}
	}

	applied := true
	switch event.Kind {
	case EventSessionStart:
		next.SessionClosed = false
		next.TurnID = ""
		next.ClosedTurnIDs = nil
		next.Children = map[string]childRecord{}
		next.AttentionCorrelation = ""
		resetCadence(&next)
		setRecordState(&next, StateWaiting, event, policy)
		next.TurnStartedAt = time.Time{}
	case EventTurnStart:
		if next.TurnID != "" && next.TurnID != event.TurnID {
			next.ClosedTurnIDs = addClosedTurn(next.ClosedTurnIDs, next.TurnID)
		}
		next.TurnID = event.TurnID
		next.AttentionCorrelation = ""
		resetCadence(&next)
		setRecordState(&next, StateWorking, event, policy)
		next.TurnStartedAt = event.ObservedAt
	case EventProgress:
		if next.State == StateCompleted && sameTurn(next.TurnID, event.TurnID) {
			return previous, Decision{Reason: "completed-dominates-progress"}
		}
		if next.State == StateAttention && (event.RawEvent != "PreToolUse" || !correlationsMatch(next.AttentionCorrelation, event.CorrelationID)) {
			return previous, Decision{Reason: "uncorrelated-progress"}
		}
		next.AttentionCorrelation = ""
		setRecordState(&next, StateWorking, event, policy)
	case EventAttentionCandidate, EventAttentionConfirmed:
		setRecordState(&next, StateAttention, event, policy)
		if event.CorrelationID != "" {
			next.AttentionCorrelation = event.CorrelationID
		}
	case EventAttentionResolved:
		if next.State == StateCompleted && sameTurn(next.TurnID, event.TurnID) {
			return previous, Decision{Reason: "completed-dominates-progress"}
		}
		if next.State == StateAttention && !correlationsMatch(next.AttentionCorrelation, event.CorrelationID) {
			return previous, Decision{Reason: "attention-correlation-mismatch"}
		}
		next.AttentionCorrelation = ""
		setRecordState(&next, StateWorking, event, policy)
	case EventTurnStop:
		if next.TurnID != "" && event.TurnID != "" && next.TurnID != event.TurnID {
			return previous, Decision{Reason: "foreign-stop"}
		}
		if event.TurnID != "" {
			next.TurnID = event.TurnID
			next.ClosedTurnIDs = addClosedTurn(next.ClosedTurnIDs, event.TurnID)
		}
		next.AttentionCorrelation = ""
		setRecordState(&next, StateCompleted, event, policy)
	case EventSessionEnd:
		next.ClosedTurnIDs = addClosedTurn(next.ClosedTurnIDs, next.TurnID)
		next.AttentionCorrelation = ""
		resetCadence(&next)
		setRecordState(&next, StateWaiting, event, policy)
		next.TurnID = ""
		next.TurnStartedAt = time.Time{}
		next.SessionClosed = true
	case EventSubagentStart, EventSubagentStop:
		return previous, Decision{Reason: "missing-child-id"}
	case EventFailure:
		if event.RawEvent == "StopFailure" {
			next.ClosedTurnIDs = addClosedTurn(next.ClosedTurnIDs, event.TurnID)
			next.AttentionCorrelation = ""
			setRecordState(&next, StateUnknown, event, policy)
			break
		}
		if next.State != StateAttention || !correlationsMatch(next.AttentionCorrelation, event.CorrelationID) {
			if sessionChanged {
				next.Sequence = previous.Sequence + 1
				next.State = StateUnknown
				next.Reason = "provider-session-replaced"
				next.RawEvent = event.RawEvent
				next.UpdatedAt = event.ObservedAt
				next.StateChangedAt = event.ObservedAt
				next.LastEventAt = event.ObservedAt
				return next, Decision{Applied: true, Reason: "provider-session-replaced"}
			}
			return previous, Decision{Reason: "failure-observed"}
		}
		next.AttentionCorrelation = ""
		setRecordState(&next, StateUnknown, event, policy)
	default:
		applied = false
	}
	if !applied {
		return previous, Decision{Reason: "unsupported-kind"}
	}
	next.Sequence = previous.Sequence + 1
	if next.Sequence == 0 {
		next.Sequence = 1
	}
	return next, Decision{Applied: true, Reason: "applied"}
}

func setRecordState(next *record, state State, event Event, policy Policy) {
	if (state == StateWorking || event.Kind == EventTurnStart) && !next.LastProgressAt.IsZero() && event.ObservedAt.After(next.LastProgressAt) {
		gap := event.ObservedAt.Sub(next.LastProgressAt)
		if gap > 0 {
			if next.ObservedGap == 0 {
				next.ObservedGap = gap
			} else {
				next.ObservedGap = (next.ObservedGap*3 + gap) / 4
			}
		}
	}
	if next.State != state {
		next.StateChangedAt = event.ObservedAt
	}
	next.State = state
	next.Reason = event.Reason
	next.RawEvent = event.RawEvent
	next.UpdatedAt = event.ObservedAt
	next.LastEventAt = event.ObservedAt
	if event.TurnID != "" {
		next.TurnID = event.TurnID
	}
	if state == StateWorking {
		next.LastProgressAt = event.ObservedAt
		next.WorkingUntil = event.ObservedAt.Add(leaseDuration(*next, policy))
	} else if state == StateWaiting {
		next.WorkingUntil = event.ObservedAt.Add(policy.WorkingTTL)
	} else {
		next.WorkingUntil = time.Time{}
	}
}

func compactSessionStartDuringActiveTurn(current record, event Event) bool {
	return event.Kind == EventSessionStart &&
		strings.EqualFold(event.Source, "compact") &&
		current.ProviderSessionID != "" &&
		current.ProviderSessionID == event.ProviderSessionID &&
		!current.SessionClosed &&
		current.TurnID != "" &&
		current.State != StateCompleted &&
		!containsTurn(current.ClosedTurnIDs, current.TurnID)
}

func resetCadence(current *record) {
	current.LastProgressAt = time.Time{}
	current.ObservedGap = 0
}

func reduceChild(previous, next record, event Event, policy Policy, sessionChanged bool) (record, Decision) {
	child, exists := next.Children[event.ChildID]
	if !exists {
		child = childRecord{ID: event.ChildID, Type: event.ChildType, TurnID: event.TurnID, State: StateUnknown}
	}
	if event.ChildType != "" {
		child.Type = event.ChildType
	}
	if child.TurnID != "" && event.TurnID != "" && child.TurnID != event.TurnID && event.Kind != EventTurnStart {
		return previous, Decision{Reason: "foreign-child-turn"}
	}
	switch event.Kind {
	case EventSubagentStart:
		child.TurnID = event.TurnID
		setChildState(&child, StateWorking, event, policy)
		child.AttentionCorrelation = ""
	case EventSubagentStop:
		child.AttentionCorrelation = ""
		setChildState(&child, StateCompleted, event, policy)
	case EventTurnStart:
		child.TurnID = event.TurnID
		setChildState(&child, StateWorking, event, policy)
		child.AttentionCorrelation = ""
	case EventProgress:
		if child.State == StateCompleted && sameTurn(child.TurnID, event.TurnID) {
			return previous, Decision{Reason: "completed-child-dominates-progress"}
		}
		if child.State == StateAttention && (event.RawEvent != "PreToolUse" || !correlationsMatch(child.AttentionCorrelation, event.CorrelationID)) {
			return previous, Decision{Reason: "uncorrelated-child-progress"}
		}
		child.AttentionCorrelation = ""
		setChildState(&child, StateWorking, event, policy)
	case EventAttentionCandidate, EventAttentionConfirmed:
		setChildState(&child, StateAttention, event, policy)
		if event.CorrelationID != "" {
			child.AttentionCorrelation = event.CorrelationID
		}
	case EventAttentionResolved:
		if child.State == StateCompleted && sameTurn(child.TurnID, event.TurnID) {
			return previous, Decision{Reason: "completed-child-dominates-progress"}
		}
		if child.State == StateAttention && !correlationsMatch(child.AttentionCorrelation, event.CorrelationID) {
			return previous, Decision{Reason: "child-attention-correlation-mismatch"}
		}
		child.AttentionCorrelation = ""
		setChildState(&child, StateWorking, event, policy)
	case EventTurnStop:
		child.AttentionCorrelation = ""
		setChildState(&child, StateCompleted, event, policy)
	case EventFailure:
		if event.RawEvent == "StopFailure" {
			child.AttentionCorrelation = ""
			setChildState(&child, StateUnknown, event, policy)
			break
		}
		if child.State != StateAttention || !correlationsMatch(child.AttentionCorrelation, event.CorrelationID) {
			if sessionChanged {
				setChildState(&child, StateUnknown, event, policy)
				break
			}
			return previous, Decision{Reason: "child-failure-observed"}
		}
		child.AttentionCorrelation = ""
		setChildState(&child, StateUnknown, event, policy)
	default:
		return previous, Decision{Reason: "unsupported-child-kind"}
	}
	next.Children[event.ChildID] = child
	next.Children = retainChildren(next.Children, event.ObservedAt, policy)
	next.Sequence = previous.Sequence + 1
	if next.Sequence == 0 {
		next.Sequence = 1
	}
	next.UpdatedAt = event.ObservedAt
	next.LastEventAt = event.ObservedAt
	next.RawEvent = event.RawEvent
	next.Reason = event.Reason
	return next, Decision{Applied: true, Reason: "applied-child"}
}

func retainChildren(children map[string]childRecord, now time.Time, policy Policy) map[string]childRecord {
	type retainedChild struct {
		key   string
		value childRecord
	}
	retained := make([]retainedChild, 0, len(children))
	for key, child := range children {
		if policy.ChildRetention > 0 && now.Sub(child.UpdatedAt) > policy.ChildRetention {
			continue
		}
		retained = append(retained, retainedChild{key: key, value: child})
	}
	sort.Slice(retained, func(i, j int) bool {
		leftRank := rankRetainedChild(retained[i].value, now)
		rightRank := rankRetainedChild(retained[j].value, now)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if !retained[i].value.UpdatedAt.Equal(retained[j].value.UpdatedAt) {
			return retained[i].value.UpdatedAt.After(retained[j].value.UpdatedAt)
		}
		return retained[i].key < retained[j].key
	})
	if len(retained) > maxRetainedChildren {
		retained = retained[:maxRetainedChildren]
	}
	result := make(map[string]childRecord, len(retained))
	for _, child := range retained {
		result[child.key] = child.value
	}
	return result
}

func rankRetainedChild(child childRecord, now time.Time) int {
	state := child.State
	if state == StateWorking && (child.WorkingUntil.IsZero() || !now.Before(child.WorkingUntil)) {
		state = StateUnknown
	}
	return rankState(state)
}

func containsOpaqueID(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func addOpaqueID(values []string, candidate string, limit int) []string {
	if candidate == "" || containsOpaqueID(values, candidate) {
		return values
	}
	values = append(values, candidate)
	if limit > 0 && len(values) > limit {
		values = append([]string(nil), values[len(values)-limit:]...)
	}
	return values
}

func setChildState(child *childRecord, state State, event Event, policy Policy) {
	if child.State != state {
		child.StateChangedAt = event.ObservedAt
	}
	child.State = state
	child.Reason = event.Reason
	child.RawEvent = event.RawEvent
	child.UpdatedAt = event.ObservedAt
	child.LastEventAt = event.ObservedAt
	if event.TurnID != "" {
		child.TurnID = event.TurnID
	}
	if event.Kind == EventSubagentStart || event.Kind == EventTurnStart {
		child.TurnStartedAt = event.ObservedAt
	}
	if state == StateWorking {
		child.WorkingUntil = event.ObservedAt.Add(policy.WorkingTTL)
	} else {
		child.WorkingUntil = time.Time{}
	}
}

func correlationsMatch(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	known := make(map[string]struct{})
	for _, value := range strings.Split(left, "|") {
		if value != "" {
			known[value] = struct{}{}
		}
	}
	for _, value := range strings.Split(right, "|") {
		if _, ok := known[value]; ok {
			return true
		}
	}
	return false
}

func leaseDuration(current record, policy Policy) time.Duration {
	floor := policy.WorkingTTL
	if floor <= 0 {
		floor = DefaultPolicy().WorkingTTL
	}
	lease := floor
	if current.ObservedGap > 0 && current.ObservedGap*3 > lease {
		lease = current.ObservedGap * 3
	}
	cap := floor * 6
	if cap < 10*time.Minute {
		cap = 10 * time.Minute
	}
	if cap > time.Hour {
		cap = time.Hour
	}
	if lease > cap {
		lease = cap
	}
	return lease
}

func resolve(current record, now time.Time, policy Policy) Annotation {
	state := current.State
	fresh := state != StateUnknown
	reason := current.Reason
	rawEvent := current.RawEvent
	updatedAt := current.UpdatedAt
	turnStartedAt := current.TurnStartedAt
	stateChangedAt := current.StateChangedAt
	lastEventAt := current.LastEventAt
	if state == StateWorking && (current.WorkingUntil.IsZero() || !now.Before(current.WorkingUntil)) {
		state, fresh = StateUnknown, false
	}
	if state == StateWaiting && (current.RawEvent == "SessionEnd" || current.WorkingUntil.IsZero() || !now.Before(current.WorkingUntil)) {
		state, fresh = StateUnknown, false
	}
	children := make([]ChildAnnotation, 0, len(current.Children))
	for _, child := range current.Children {
		if policy.ChildRetention > 0 && now.Sub(child.UpdatedAt) > policy.ChildRetention {
			continue
		}
		childState := child.State
		childFresh := true
		if childState == StateWorking && (child.WorkingUntil.IsZero() || !now.Before(child.WorkingUntil)) {
			childState, childFresh = StateUnknown, false
		}
		children = append(children, ChildAnnotation{
			ID: child.ID, Type: child.Type, State: childState, Reason: child.Reason, RawEvent: child.RawEvent,
			UpdatedAt: child.UpdatedAt, TurnStartedAt: child.TurnStartedAt, StateChangedAt: child.StateChangedAt,
			LastEventAt: child.LastEventAt, Fresh: childFresh,
		})
	}
	sort.SliceStable(children, func(i, j int) bool {
		if rankState(children[i].State) != rankState(children[j].State) {
			return rankState(children[i].State) < rankState(children[j].State)
		}
		return children[i].UpdatedAt.After(children[j].UpdatedAt)
	})
	for _, child := range children {
		if child.Fresh && rankState(child.State) < rankState(state) {
			state, fresh = child.State, true
			reason = child.Reason
			rawEvent = child.RawEvent
			updatedAt = child.UpdatedAt
			turnStartedAt = child.TurnStartedAt
			stateChangedAt = child.StateChangedAt
			lastEventAt = child.LastEventAt
		}
	}
	annotation := Annotation{
		Provider: current.Provider, ProviderSessionID: current.ProviderSessionID, TurnID: current.TurnID,
		Pane: current.Pane, State: state, Reason: reason, Source: "hook", RawEvent: rawEvent, UpdatedAt: updatedAt,
		TurnStartedAt: turnStartedAt, StateChangedAt: stateChangedAt, LastEventAt: lastEventAt,
		Fresh: fresh, Children: children,
	}
	if state == StateCompleted {
		annotation.AcknowledgeToken = acknowledgementToken(current)
	}
	return annotation
}

func acknowledgementToken(current record) string {
	var high time.Time
	for _, child := range current.Children {
		if child.UpdatedAt.After(high) {
			high = child.UpdatedAt
		}
	}
	data := recordKey(current.Pane, current.Provider) + "\x00" + current.ProviderSessionID + "\x00" + current.TurnID + "\x00" + time.Unix(0, int64(current.Sequence)).UTC().Format(time.RFC3339Nano) + "\x00" + high.UTC().Format(time.RFC3339Nano)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func rankState(state State) int {
	switch state {
	case StateAttention:
		return 0
	case StateWorking:
		return 1
	case StateCompleted:
		return 2
	case StateWaiting:
		return 3
	default:
		return 4
	}
}

func sameTurn(current, incoming string) bool {
	return current == "" || incoming == "" || current == incoming
}
func containsTurn(turns []string, turn string) bool {
	for _, item := range turns {
		if item == turn {
			return true
		}
	}
	return false
}
func addClosedTurn(turns []string, turn string) []string {
	if turn == "" || containsTurn(turns, turn) {
		return turns
	}
	turns = append(turns, turn)
	if len(turns) > 16 {
		turns = append([]string(nil), turns[len(turns)-16:]...)
	}
	return turns
}
