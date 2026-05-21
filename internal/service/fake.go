package service

import "context"

// Fake is an in-memory Backend for tests. Populate States with the desired
// response for each name; names absent from States produce ErrNotFound.
type Fake struct {
	States map[string]State
}

func (f *Fake) Status(_ context.Context, name string) (State, error) {
	if f.States == nil {
		return "", ErrNotFound
	}
	s, ok := f.States[name]
	if !ok {
		return "", ErrNotFound
	}
	return s, nil
}
