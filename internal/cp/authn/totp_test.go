package authn

import (
	"testing"
	"time"
)

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
