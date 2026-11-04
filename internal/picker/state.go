package picker

import "context"

// State belongs to one view for the lifetime of its popup.
type State struct {
	Query    string `json:"query"`
	Selected string `json:"selected"`
}

type stateKey struct{}

func WithState(ctx context.Context, state *State) context.Context {
	return context.WithValue(ctx, stateKey{}, state)
}

func StateFromContext(ctx context.Context) *State {
	state, _ := ctx.Value(stateKey{}).(*State)
	return state
}

func itemStateKey[T any](item Item[T]) string {
	if item.Preview != "" {
		return item.Preview
	}
	return item.Label
}
