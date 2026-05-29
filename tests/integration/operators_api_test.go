package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

// doJSON issues an authed JSON request and returns the response.
func doJSON(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rdr)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", method+url) // mutating routes require it
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// TestOperatorsAPIStaffGate — a non-staff operator is forbidden from the
// operator-management surface; a staff operator can list.
func TestOperatorsAPIStaffGate(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// Staff token via the shared helper.
	staffTok := mintAccessToken(t, ctx, srv)
	resp := doJSON(t, http.MethodGet, srv.URL+"/operators", staffTok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("staff GET /operators = %d; body=%s", resp.StatusCode, raw)
	}

	// A non-staff but TOTP-enrolled operator: forbidden.
	_, scopedTok := enrolledOperator(t, ctx, srv, "scoped@acme.test", false)
	resp2 := doJSON(t, http.MethodGet, srv.URL+"/operators", scopedTok, nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("non-staff GET /operators = %d, want 403", resp2.StatusCode)
	}
}

// TestOperatorsAPIMustChangeFlow — the end-to-end onboarding flow: staff
// creates an operator (one-time temp password), the new operator logs in
// (must-change signalled), is blocked from TOTP enrollment until they set a
// new password via POST /auth/password, after which enrollment is reachable.
func TestOperatorsAPIMustChangeFlow(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	staffTok := mintAccessToken(t, ctx, srv)

	// Create.
	resp := doJSON(t, http.MethodPost, srv.URL+"/operators", staffTok, map[string]any{
		"email": "coworker@acme.test", "is_staff": false,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /operators = %d; body=%s", resp.StatusCode, raw)
	}
	var created struct {
		Operator     struct{ ID string } `json:"operator"`
		TempPassword string              `json:"temp_password"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.TempPassword == "" {
		t.Fatal("no temp password returned")
	}

	// New operator logs in: must-change signalled, not yet enrolled.
	login, err := srv.AuthN.Login(ctx, authn.LoginInput{Email: "coworker@acme.test", Password: created.TempPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !login.MustChangePassword {
		t.Fatal("login did not signal must-change")
	}
	newTok := login.Tokens.AccessToken

	// TOTP enrollment is blocked until the password is rotated.
	blocked := doJSON(t, http.MethodPost, srv.URL+"/auth/totp/enroll", newTok, nil)
	defer blocked.Body.Close()
	if blocked.StatusCode != http.StatusForbidden || blocked.Header.Get("Reason") != "password-change-required" {
		t.Errorf("totp/enroll while must-change = %d (Reason %q), want 403 password-change-required",
			blocked.StatusCode, blocked.Header.Get("Reason"))
	}

	// Set a new password — reachable despite must-change.
	setPw := doJSON(t, http.MethodPost, srv.URL+"/auth/password", newTok, map[string]any{
		"new_password": "a-much-stronger-passphrase",
	})
	defer setPw.Body.Close()
	if setPw.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(setPw.Body)
		t.Fatalf("POST /auth/password = %d; body=%s", setPw.StatusCode, raw)
	}

	// Now TOTP enrollment is reachable (no longer 403 for password reasons).
	afterTok := newTok
	enroll := doJSON(t, http.MethodPost, srv.URL+"/auth/totp/enroll", afterTok, nil)
	defer enroll.Body.Close()
	if enroll.StatusCode == http.StatusForbidden && enroll.Header.Get("Reason") == "password-change-required" {
		t.Errorf("totp/enroll still blocked on password after set-password")
	}
}
