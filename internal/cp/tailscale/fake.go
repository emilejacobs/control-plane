package tailscale

import "context"

// Fake is an in-memory Minter for tests and offline runs. It records the mint
// requests it received and returns a canned key (or a configured error).
type Fake struct {
	Minted      []MintOptions
	KeyToReturn Key
	Err         error
}

// NewFake returns a Fake that mints a placeholder key.
func NewFake() *Fake {
	return &Fake{KeyToReturn: Key{ID: "fake-key-id", Key: "tskey-auth-fake"}}
}

// MintAuthKey records opts and returns the canned key (or Err).
func (f *Fake) MintAuthKey(_ context.Context, opts MintOptions) (Key, error) {
	f.Minted = append(f.Minted, opts)
	if f.Err != nil {
		return Key{}, f.Err
	}
	return f.KeyToReturn, nil
}
