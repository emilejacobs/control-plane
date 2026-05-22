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
)

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
