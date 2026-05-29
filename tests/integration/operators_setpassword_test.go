package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/operators"
)

// TestSetPasswordClearsMustChange — setting a new password clears the
// must-change flag, makes the new password work, and invalidates the old
// temp password. A too-short password is rejected.
func TestSetPasswordClearsMustChange(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	store := operators.New(srv.Pool)

	res, err := store.Create(ctx, operators.CreateInput{Email: "rotate@acme.test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := srv.AuthN.SetPassword(ctx, res.Operator.ID, "short"); !errors.Is(err, authn.ErrWeakPassword) {
		t.Errorf("SetPassword(short) err = %v, want ErrWeakPassword", err)
	}

	const newPassword = "a-much-stronger-passphrase"
	if err := srv.AuthN.SetPassword(ctx, res.Operator.ID, newPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	mustChange, err := srv.AuthN.MustChangePassword(ctx, res.Operator.ID)
	if err != nil {
		t.Fatalf("MustChangePassword: %v", err)
	}
	if mustChange {
		t.Error("MustChangePassword = true after SetPassword, want false")
	}

	login, err := srv.AuthN.Login(ctx, authn.LoginInput{Email: "rotate@acme.test", Password: newPassword})
	if err != nil {
		t.Fatalf("Login with new password: %v", err)
	}
	if login.MustChangePassword {
		t.Error("login still flags must-change after rotation")
	}

	if _, err := srv.AuthN.Login(ctx, authn.LoginInput{Email: "rotate@acme.test", Password: res.TempPassword}); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Errorf("login with old temp password err = %v, want ErrInvalidCredentials", err)
	}
}
