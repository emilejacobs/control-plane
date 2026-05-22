package authn

import (
	"testing"
	"time"
)

func TestValidateTotpDriftWindow(t *testing.T) {
	secret := newTotpSecret()
	at := time.Date(2026, 5, 21, 12, 0, 30, 0, time.UTC)
	step := totpPeriod * time.Second

	codeAt := func(offset time.Duration) string {
		t.Helper()
		c, err := totpCodeAt(secret, at.Add(offset))
		if err != nil {
			t.Fatalf("totpCodeAt: %v", err)
		}
		return c
	}

	// ±1 step of clock drift is tolerated (RFC 6238 / ADR-010).
	if !validateTotp(secret, codeAt(-step), at) {
		t.Errorf("code from the previous 30s step should validate")
	}
	if !validateTotp(secret, codeAt(step), at) {
		t.Errorf("code from the next 30s step should validate")
	}
	// ±2 steps is outside the window and must be rejected.
	if validateTotp(secret, codeAt(-2*step), at) {
		t.Errorf("code from two steps back should be rejected")
	}
	if validateTotp(secret, codeAt(2*step), at) {
		t.Errorf("code from two steps forward should be rejected")
	}
}

func TestValidateTotpAcceptsCurrentCode(t *testing.T) {
	secret := newTotpSecret()
	at := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	code, err := totpCodeAt(secret, at)
	if err != nil {
		t.Fatalf("totpCodeAt: %v", err)
	}

	if !validateTotp(secret, code, at) {
		t.Errorf("code generated for %v should validate at the same instant", at)
	}
}
