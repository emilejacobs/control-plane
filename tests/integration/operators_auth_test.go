package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/operators"
)

// TestLoginMustChangePassword — an operator created with a generated temp
// password logs in successfully (so they can reach the set-password flow),
// and the result signals must-change-password alongside the existing
// not-yet-enrolled signal.
func TestLoginMustChangePassword(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	store := operators.New(srv.Pool)

	res, err := store.Create(ctx, operators.CreateInput{Email: "newop@acme.test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	login, err := srv.AuthN.Login(ctx, authn.LoginInput{
		Email:    "newop@acme.test",
		Password: res.TempPassword,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !login.MustChangePassword {
		t.Error("MustChangePassword = false, want true on a freshly-created operator")
	}
	if !login.RequiresTotpEnrollment {
		t.Error("RequiresTotpEnrollment = false, want true (no TOTP yet)")
	}
}

// TestLoginDeactivatedRejected — a deactivated operator cannot authenticate;
// the correct password yields ErrInvalidCredentials (state not revealed).
func TestLoginDeactivatedRejected(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	store := operators.New(srv.Pool)

	res, err := store.Create(ctx, operators.CreateInput{Email: "gone@acme.test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := srv.Pool.Exec(ctx,
		`UPDATE operators SET deactivated_at = now() WHERE id = $1`, res.Operator.ID,
	); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	_, err = srv.AuthN.Login(ctx, authn.LoginInput{Email: "gone@acme.test", Password: res.TempPassword})
	if !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Errorf("Login(deactivated) err = %v, want ErrInvalidCredentials", err)
	}
}
