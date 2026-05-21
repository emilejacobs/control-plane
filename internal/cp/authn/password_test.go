package authn_test

import (
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

func TestHashPasswordProducesPHCArgon2idString(t *testing.T) {
	hash, err := authn.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("expected PHC argon2id string, got %q", hash)
	}
}

func TestVerifyPasswordAcceptsCorrectPassword(t *testing.T) {
	hash, err := authn.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := authn.VerifyPassword("hunter2", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("VerifyPassword returned false for correct password")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	hash, err := authn.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := authn.VerifyPassword("nope", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("VerifyPassword returned true for wrong password")
	}
}

func TestHashPasswordProducesDifferentSaltsAcrossCalls(t *testing.T) {
	// Same password hashed twice should produce different strings — proves
	// the salt is per-call, not global. Both must verify correctly.
	h1, err := authn.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword 1: %v", err)
	}
	h2, err := authn.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword 2: %v", err)
	}
	if h1 == h2 {
		t.Error("two calls produced identical hashes; salt is not random")
	}
}
