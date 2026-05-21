package service

import "context"

// Fake is an in-memory Backend for tests.
//
//   - Status: populate States with the desired result for each name;
//     names absent from States produce ErrNotFound.
//   - Restart: by default returns nil (success). Populate RestartErrors[name]
//     to make Restart return that error for the named service.
type Fake struct {
	States        map[string]State
	RestartErrors map[string]error
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

func (f *Fake) Restart(_ context.Context, name string) error {
	if f.RestartErrors == nil {
		return nil
	}
	return f.RestartErrors[name]
}
