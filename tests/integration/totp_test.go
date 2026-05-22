package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// totpCode stands in for an authenticator app: it computes the 6-digit TOTP
// for secret at time at.
func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period:    30,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	return code
}

// doLoginTotp POSTs /auth/login with optional totp_code / recovery_code
// fields. The caller owns resp.Body.
func doLoginTotp(t *testing.T, baseURL, email, password, totpCode, recoveryCode, idempotencyKey string) *http.Response {
	t.Helper()
	payload := map[string]any{"email": email, "password": password}
	if totpCode != "" {
		payload["totp_code"] = totpCode
	}
	if recoveryCode != "" {
		payload["recovery_code"] = recoveryCode
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// secretFromURI lifts the base32 TOTP secret out of an otpauth:// URI.
func secretFromURI(t *testing.T, provisioningURI string) string {
	t.Helper()
	u, err := url.Parse(provisioningURI)
	if err != nil {
		t.Fatalf("parse provisioning_uri %q: %v", provisioningURI, err)
	}
	secret := u.Query().Get("secret")
	if secret == "" {
		t.Fatalf("provisioning_uri carries no secret: %q", provisioningURI)
	}
	return secret
}

// firstRunToken does POST /auth/first-run and returns the access token of the
// freshly-created admin operator — the bearer token for authenticated calls.
func firstRunToken(t *testing.T, srv *testServer, email, password string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"email": email, "password": password})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/auth/first-run", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "00000000-0000-4000-8000-00000000f001")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("first-run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("first-run setup: got %d want 201; body=%s", resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode first-run: %v", err)
	}
	return out.AccessToken
}

// doTotpEnroll POSTs /auth/totp/enroll with a bearer token. The caller owns
// resp.Body.
func doTotpEnroll(t *testing.T, baseURL, token, idempotencyKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/auth/totp/enroll", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// totpEnrollResponse mirrors the POST /auth/totp/enroll success body.
type totpEnrollResponse struct {
	ProvisioningURI string   `json:"provisioning_uri"`
	RecoveryCodes   []string `json:"recovery_codes"`
}

// enrollAndSecret first-runs an admin, enrolls TOTP, and returns the email,
// password, and the TOTP shared secret lifted from the provisioning URI.
func enrollAndSecret(t *testing.T, srv *testServer) (email, password, secret string) {
	t.Helper()
	email, password = "admin@acmecorp.test", "correct-horse-battery-staple"
	token := firstRunToken(t, srv, email, password)
	resp := doTotpEnroll(t, srv.URL, token, "00000000-0000-4000-8000-000000000e01")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("enroll setup: got %d want 200; body=%s", resp.StatusCode, raw)
	}
	var out totpEnrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode enroll: %v", err)
	}
	return email, password, secretFromURI(t, out.ProvisioningURI)
}

func TestLoginRequiresTotpAfterEnrollment(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	clock := newFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	srv := newTestServerCfg(t, ctx, authn.Config{Now: clock.Now})

	email, password, secret := enrollAndSecret(t, srv)

	// A valid TOTP code for the server's clock is accepted.
	good := doLoginTotp(t, srv.URL, email, password,
		totpCode(t, secret, clock.Now()), "", "00000000-0000-4000-8000-0000000071a1")
	defer good.Body.Close()
	if good.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(good.Body)
		t.Errorf("login with valid TOTP: got %d want 200; body=%s", good.StatusCode, raw)
	}

	// A code from an hour away — far outside the ±1 window — is rejected.
	bad := doLoginTotp(t, srv.URL, email, password,
		totpCode(t, secret, clock.Now().Add(time.Hour)), "", "00000000-0000-4000-8000-0000000071a2")
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Errorf("login with stale TOTP: got %d want 401", bad.StatusCode)
	}

	// No TOTP code at all is also rejected for an enrolled operator.
	missing := doLoginTotp(t, srv.URL, email, password, "", "", "00000000-0000-4000-8000-0000000071a3")
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusUnauthorized {
		t.Errorf("login with no TOTP: got %d want 401", missing.StatusCode)
	}
}

func TestLoginWithRecoveryCodeSingleUse(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	const email = "admin@acmecorp.test"
	const password = "correct-horse-battery-staple"
	token := firstRunToken(t, srv, email, password)

	enroll := doTotpEnroll(t, srv.URL, token, "00000000-0000-4000-8000-000000000e01")
	var enr totpEnrollResponse
	if err := json.NewDecoder(enroll.Body).Decode(&enr); err != nil {
		t.Fatalf("decode enroll: %v", err)
	}
	enroll.Body.Close()
	if len(enr.RecoveryCodes) != 10 {
		t.Fatalf("expected 10 recovery codes, got %d", len(enr.RecoveryCodes))
	}
	code := enr.RecoveryCodes[3]

	// A recovery code stands in for the TOTP code.
	first := doLoginTotp(t, srv.URL, email, password, "", code, "00000000-0000-4000-8000-0000000072a1")
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(first.Body)
		t.Errorf("login with recovery code: got %d want 200; body=%s", first.StatusCode, raw)
	}

	// The same code is single-use — rejected on reuse.
	second := doLoginTotp(t, srv.URL, email, password, "", code, "00000000-0000-4000-8000-0000000072a2")
	defer second.Body.Close()
	if second.StatusCode != http.StatusUnauthorized {
		t.Errorf("reused recovery code: got %d want 401", second.StatusCode)
	}

	// A still-unused code from the same batch keeps working.
	third := doLoginTotp(t, srv.URL, email, password, "", enr.RecoveryCodes[7], "00000000-0000-4000-8000-0000000072a3")
	defer third.Body.Close()
	if third.StatusCode != http.StatusOK {
		t.Errorf("login with an unused recovery code: got %d want 200", third.StatusCode)
	}
}

func TestTotpSecretEncryptedAtRest(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	const email = "admin@acmecorp.test"
	token := firstRunToken(t, srv, email, "correct-horse-battery-staple")

	resp := doTotpEnroll(t, srv.URL, token, "00000000-0000-4000-8000-000000000e01")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enroll: got %d want 200", resp.StatusCode)
	}
	var out totpEnrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rawSecret := secretFromURI(t, out.ProvisioningURI)

	// The stored column must be ciphertext: the raw shared secret must not
	// appear in it verbatim.
	var stored []byte
	if err := srv.Pool.QueryRow(ctx,
		`SELECT totp_secret_encrypted FROM operators WHERE email = $1`, email,
	).Scan(&stored); err != nil {
		t.Fatalf("query totp_secret_encrypted: %v", err)
	}
	if len(stored) == 0 {
		t.Fatal("totp_secret_encrypted is empty")
	}
	if bytes.Contains(stored, []byte(rawSecret)) {
		t.Errorf("totp_secret_encrypted contains the raw secret in plaintext")
	}
}

func TestTotpEnrollSecondCallConflicts(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	token := firstRunToken(t, srv, "admin@acmecorp.test", "correct-horse-battery-staple")

	first := doTotpEnroll(t, srv.URL, token, "00000000-0000-4000-8000-000000000e01")
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first enroll: got %d want 200", first.StatusCode)
	}

	// A distinct Idempotency-Key, so this reaches the handler rather than
	// replaying the cached first response.
	second := doTotpEnroll(t, srv.URL, token, "00000000-0000-4000-8000-000000000e02")
	second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second enroll: got %d want 409", second.StatusCode)
	}
}

func TestTotpEnrollHappyPath(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	const email = "admin@acmecorp.test"
	token := firstRunToken(t, srv, email, "correct-horse-battery-staple")

	resp := doTotpEnroll(t, srv.URL, token, "00000000-0000-4000-8000-000000000e01")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("enroll: got %d want 200; body=%s", resp.StatusCode, raw)
	}

	var out totpEnrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(out.ProvisioningURI, "otpauth://totp/") {
		t.Errorf("provisioning_uri is not an otpauth URI: %q", out.ProvisioningURI)
	}
	if len(out.RecoveryCodes) != 10 {
		t.Errorf("recovery_codes: got %d want 10", len(out.RecoveryCodes))
	}

	// The operator row now carries an encrypted TOTP secret.
	var secretLen int
	if err := srv.Pool.QueryRow(ctx,
		`SELECT coalesce(octet_length(totp_secret_encrypted), 0) FROM operators WHERE email = $1`,
		email,
	).Scan(&secretLen); err != nil {
		t.Fatalf("query totp_secret_encrypted: %v", err)
	}
	if secretLen == 0 {
		t.Errorf("totp_secret_encrypted is empty after enrollment")
	}
}
