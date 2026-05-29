package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/operators"
)

// TestOperatorsStoreCreate — creating an operator generates a one-time temp
// password (returned in plaintext, persisted only as a hash), arms
// must_change_password, and records the site allowlist. A duplicate email is
// rejected with ErrEmailTaken.
func TestOperatorsStoreCreate(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	store := operators.New(srv.Pool)

	clientID := insertClient(t, ctx, srv, "AcmeCorp")
	siteA := insertSite(t, ctx, srv, clientID, "Store 54")
	siteB := insertSite(t, ctx, srv, clientID, "Store 12")

	res, err := store.Create(ctx, operators.CreateInput{
		Email:   "  NewOp@Acme.test ",
		IsStaff: false,
		SiteIDs: []string{siteA, siteB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.TempPassword == "" {
		t.Fatal("temp password is empty, want a generated one")
	}
	if res.Operator.Email != "newop@acme.test" {
		t.Errorf("email = %q, want normalized newop@acme.test", res.Operator.Email)
	}
	if res.Operator.IsStaff {
		t.Error("IsStaff = true, want false")
	}
	if len(res.Operator.SiteIDs) != 2 {
		t.Errorf("site allowlist = %v, want 2 sites", res.Operator.SiteIDs)
	}

	// The persisted hash verifies against the returned plaintext, and the
	// plaintext is never stored as-is.
	var hash string
	var mustChange bool
	if err := srv.Pool.QueryRow(ctx,
		`SELECT password_hash, must_change_password FROM operators WHERE id = $1`,
		res.Operator.ID,
	).Scan(&hash, &mustChange); err != nil {
		t.Fatalf("select created operator: %v", err)
	}
	if !mustChange {
		t.Error("must_change_password = false, want true on create")
	}
	if hash == res.TempPassword {
		t.Error("password stored in plaintext")
	}
	ok, err := authn.VerifyPassword(res.TempPassword, hash)
	if err != nil || !ok {
		t.Errorf("temp password does not verify against stored hash (ok=%v err=%v)", ok, err)
	}

	// Duplicate email (case-insensitive) is rejected.
	if _, err := store.Create(ctx, operators.CreateInput{Email: "newop@acme.test"}); !errors.Is(err, operators.ErrEmailTaken) {
		t.Errorf("duplicate create err = %v, want ErrEmailTaken", err)
	}
}
