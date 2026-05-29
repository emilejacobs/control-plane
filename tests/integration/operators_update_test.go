package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/operators"
)

func ptr[T any](v T) *T { return &v }

// TestOperatorsStoreUpdate — edit toggles is_staff, replaces the site
// allowlist, and resets TOTP (forcing re-enrollment).
func TestOperatorsStoreUpdate(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	store := operators.New(srv.Pool)

	clientID := insertClient(t, ctx, srv, "AcmeCorp")
	siteA := insertSite(t, ctx, srv, clientID, "Store 54")
	siteB := insertSite(t, ctx, srv, clientID, "Store 12")

	res, err := store.Create(ctx, operators.CreateInput{Email: "edit@acme.test", SiteIDs: []string{siteA}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := res.Operator.ID

	// Simulate an enrolled TOTP secret so ResetTotp has something to clear.
	if _, err := srv.Pool.Exec(ctx,
		`UPDATE operators SET totp_secret_encrypted = $2 WHERE id = $1`, id, []byte("ciphertext"),
	); err != nil {
		t.Fatalf("seed totp: %v", err)
	}

	updated, err := store.Update(ctx, id, operators.UpdateInput{
		IsStaff:   ptr(true),
		SiteIDs:   ptr([]string{siteB}),
		ResetTotp: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.IsStaff {
		t.Error("IsStaff = false, want true after promote")
	}
	if updated.TotpEnrolled {
		t.Error("TotpEnrolled = true, want false after ResetTotp")
	}
	if len(updated.SiteIDs) != 1 || updated.SiteIDs[0] != siteB {
		t.Errorf("allowlist = %v, want [%s]", updated.SiteIDs, siteB)
	}

	if _, err := store.Update(ctx, "00000000-0000-0000-0000-0000000000ff", operators.UpdateInput{IsStaff: ptr(false)}); !errors.Is(err, operators.ErrNotFound) {
		t.Errorf("Update(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestOperatorsStoreResetPasswordAndActive — reset password re-arms
// must-change with a fresh temp password; deactivate/reactivate flip the
// soft-delete flag.
func TestOperatorsStoreResetPasswordAndActive(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	store := operators.New(srv.Pool)

	res, err := store.Create(ctx, operators.CreateInput{Email: "reset@acme.test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := res.Operator.ID

	// Clear must-change (as if the operator had rotated their temp pw) so we
	// can observe ResetPassword re-arming it.
	if _, err := srv.Pool.Exec(ctx, `UPDATE operators SET must_change_password = false WHERE id = $1`, id); err != nil {
		t.Fatalf("clear must-change: %v", err)
	}

	newTemp, err := store.ResetPassword(ctx, id)
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if newTemp == "" || newTemp == res.TempPassword {
		t.Errorf("reset temp pw = %q, want a fresh non-empty value", newTemp)
	}
	var hash string
	var mustChange bool
	if err := srv.Pool.QueryRow(ctx,
		`SELECT password_hash, must_change_password FROM operators WHERE id = $1`, id,
	).Scan(&hash, &mustChange); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !mustChange {
		t.Error("must_change_password = false after reset, want true")
	}
	if ok, _ := authn.VerifyPassword(newTemp, hash); !ok {
		t.Error("reset temp password does not verify against stored hash")
	}

	if err := store.SetActive(ctx, id, false); err != nil {
		t.Fatalf("SetActive(false): %v", err)
	}
	if got, _ := store.Get(ctx, id); !got.Deactivated {
		t.Error("Deactivated = false after SetActive(false)")
	}
	if err := store.SetActive(ctx, id, true); err != nil {
		t.Fatalf("SetActive(true): %v", err)
	}
	if got, _ := store.Get(ctx, id); got.Deactivated {
		t.Error("Deactivated = true after reactivate")
	}

	if err := store.SetActive(ctx, "00000000-0000-0000-0000-0000000000ff", false); !errors.Is(err, operators.ErrNotFound) {
		t.Errorf("SetActive(unknown) err = %v, want ErrNotFound", err)
	}
	if _, err := store.ResetPassword(ctx, "00000000-0000-0000-0000-0000000000ff"); !errors.Is(err, operators.ErrNotFound) {
		t.Errorf("ResetPassword(unknown) err = %v, want ErrNotFound", err)
	}
}
