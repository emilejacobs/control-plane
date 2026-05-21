package authn_test

import (
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

func testKey() []byte {
	return []byte("test-signing-key-must-be-at-least-32-bytes-zzzz")
}

func TestSignerIssueProducesThreeSegmentJWT(t *testing.T) {
	s := authn.NewSigner(testKey(), time.Hour)
	tok, err := s.Issue(authn.TokenClaims{
		OperatorID: "11111111-2222-3333-4444-555555555555",
		Email:      "admin@uknomi.test",
		IsStaff:    true,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if c := strings.Count(tok, "."); c != 2 {
		t.Errorf("expected 3 dot-separated segments, got %d (token=%q)", c+1, tok)
	}
}

func TestSignerVerifyAcceptsItsOwnToken(t *testing.T) {
	s := authn.NewSigner(testKey(), time.Hour)
	tok, err := s.Issue(authn.TokenClaims{
		OperatorID: "11111111-2222-3333-4444-555555555555",
		Email:      "admin@uknomi.test",
		IsStaff:    true,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.OperatorID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("OperatorID: got %q", claims.OperatorID)
	}
	if claims.Email != "admin@uknomi.test" {
		t.Errorf("Email: got %q", claims.Email)
	}
	if !claims.IsStaff {
		t.Error("IsStaff: got false, want true")
	}
}

func TestSignerVerifyRejectsWrongSignature(t *testing.T) {
	signerA := authn.NewSigner(testKey(), time.Hour)
	signerB := authn.NewSigner([]byte("different-key-also-at-least-32-bytes-yyyy"), time.Hour)
	tok, _ := signerA.Issue(authn.TokenClaims{OperatorID: "x", Email: "y", IsStaff: false})
	if _, err := signerB.Verify(tok); err == nil {
		t.Error("expected Verify to reject token signed with a different key")
	}
}

func TestSignerVerifyRejectsTamperedToken(t *testing.T) {
	s := authn.NewSigner(testKey(), time.Hour)
	tok, _ := s.Issue(authn.TokenClaims{OperatorID: "x", Email: "y", IsStaff: false})
	// Flip a character in the payload segment to invalidate the signature.
	segments := strings.Split(tok, ".")
	if len(segments) != 3 {
		t.Fatalf("token has wrong shape: %q", tok)
	}
	tampered := segments[0] + "." + segments[1] + "X." + segments[2]
	if _, err := s.Verify(tampered); err == nil {
		t.Error("expected Verify to reject a tampered token")
	}
}
