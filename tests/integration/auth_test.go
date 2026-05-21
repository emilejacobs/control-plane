package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

// TestFirstRunHappyPath is Issue 04 cycle 3: a POST /auth/first-run on a
// fresh deployment creates the admin operator, mints an access + refresh
// token pair, persists the refresh token, and writes an audit log line.
func TestFirstRunHappyPath(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	const reqEmail = "First.Admin@AcmeCorp.test"
	const wantEmail = "first.admin@acmecorp.test"

	body, err := json.Marshal(map[string]any{
		"email":    reqEmail,
		"password": "correct-horse-battery-staple",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/auth/first-run", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "00000000-0000-0000-0000-0000000000f1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 201; body=%s", resp.StatusCode, raw)
	}

	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AccessToken == "" {
		t.Errorf("access_token is empty")
	}
	if out.RefreshToken == "" {
		t.Errorf("refresh_token is empty")
	}

	// The admin operator row exists, with a normalized email, a hashed
	// password, and is_staff set.
	var operatorID, gotEmail, passwordHash string
	var isStaff bool
	err = srv.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, is_staff FROM operators`,
	).Scan(&operatorID, &gotEmail, &passwordHash, &isStaff)
	if err != nil {
		t.Fatalf("query operators row: %v", err)
	}
	if gotEmail != wantEmail {
		t.Errorf("email: got %q want %q (should be lowercased)", gotEmail, wantEmail)
	}
	if !isStaff {
		t.Errorf("is_staff: got false want true")
	}
	if passwordHash == "" || passwordHash == "correct-horse-battery-staple" {
		t.Errorf("password_hash not a hash: %q", passwordHash)
	}

	// The refresh token was persisted (hashed) against that operator.
	var refreshCount int
	if err := srv.Pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE operator_id = $1 AND revoked_at IS NULL`,
		operatorID,
	).Scan(&refreshCount); err != nil {
		t.Fatalf("query refresh_tokens: %v", err)
	}
	if refreshCount != 1 {
		t.Errorf("refresh_tokens rows for operator: got %d want 1", refreshCount)
	}

	// An audit log line records the successful first-run claim.
	if !auditLogged(srv.Logs.String(), "audit.first_run", map[string]any{
		"outcome": "success",
		"email":   reqEmail,
	}) {
		t.Errorf("no audit.first_run success log line found.\nFull log buffer:\n%s", srv.Logs.String())
	}
}

// TestFirstRunSecondCallGone is Issue 04 cycle 4: once the system is
// initialized, a second POST /auth/first-run is rejected with 410 Gone and
// no second operator row is created.
func TestFirstRunSecondCallGone(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// First claim succeeds.
	if code := doFirstRun(t, srv.URL, "admin@acmecorp.test", "correct-horse-battery-staple",
		"00000000-0000-0000-0000-0000000000f1"); code != http.StatusCreated {
		t.Fatalf("first claim: got %d want 201", code)
	}

	// Second claim — distinct email and Idempotency-Key so it reaches the
	// handler rather than replaying the cached first response — is denied.
	const secondEmail = "intruder@acmecorp.test"
	code := doFirstRun(t, srv.URL, secondEmail, "another-password-entirely",
		"00000000-0000-0000-0000-0000000000f2")
	if code != http.StatusGone {
		t.Fatalf("second claim: got %d want 410", code)
	}

	// Still exactly one operator, and it is not the intruder.
	var count int
	if err := srv.Pool.QueryRow(ctx, `SELECT count(*) FROM operators`).Scan(&count); err != nil {
		t.Fatalf("count operators: %v", err)
	}
	if count != 1 {
		t.Errorf("operator rows: got %d want 1", count)
	}
	var intruderExists bool
	if err := srv.Pool.QueryRow(ctx,
		`SELECT exists(SELECT 1 FROM operators WHERE email = $1)`, secondEmail,
	).Scan(&intruderExists); err != nil {
		t.Fatalf("check intruder: %v", err)
	}
	if intruderExists {
		t.Errorf("second first-run created an operator row %q", secondEmail)
	}

	// The denied attempt is audit-logged.
	if !auditLogged(srv.Logs.String(), "audit.first_run", map[string]any{
		"outcome": "denied",
		"reason":  "already_initialized",
		"email":   secondEmail,
	}) {
		t.Errorf("no audit.first_run denied log line found.\nFull log buffer:\n%s", srv.Logs.String())
	}
}

// doFirstRun POSTs /auth/first-run and returns the status code.
func doFirstRun(t *testing.T, baseURL, email, password, idempotencyKey string) int {
	t.Helper()
	body, err := json.Marshal(map[string]any{"email": email, "password": password})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/auth/first-run", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// TestLoginHappyPath is Issue 04 cycle 5: after first-run bootstrap, POST
// /auth/login with correct credentials returns 200 with a fresh token pair,
// records last_login_at, and is audit-logged.
func TestLoginHappyPath(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	const email = "operator@acmecorp.test"
	const password = "correct-horse-battery-staple"
	if code := doFirstRun(t, srv.URL, email, password,
		"00000000-0000-0000-0000-0000000000f1"); code != http.StatusCreated {
		t.Fatalf("first-run setup: got %d want 201", code)
	}

	// Log in with mixed-case email to also exercise case-insensitive lookup.
	const loginEmail = "Operator@AcmeCorp.test"
	resp := doLogin(t, srv.URL, loginEmail, password, "00000000-0000-0000-0000-00000000051a")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, raw)
	}

	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AccessToken == "" {
		t.Errorf("access_token is empty")
	}
	if out.RefreshToken == "" {
		t.Errorf("refresh_token is empty")
	}

	// A successful login stamps last_login_at.
	var operatorID string
	var lastLogin *time.Time
	if err := srv.Pool.QueryRow(ctx,
		`SELECT id, last_login_at FROM operators WHERE email = $1`, email,
	).Scan(&operatorID, &lastLogin); err != nil {
		t.Fatalf("query operator: %v", err)
	}
	if lastLogin == nil {
		t.Errorf("last_login_at not set after successful login")
	}

	// first-run issued one refresh token; the login issued a second.
	var refreshCount int
	if err := srv.Pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE operator_id = $1`, operatorID,
	).Scan(&refreshCount); err != nil {
		t.Fatalf("query refresh_tokens: %v", err)
	}
	if refreshCount != 2 {
		t.Errorf("refresh_tokens rows: got %d want 2", refreshCount)
	}

	if !auditLogged(srv.Logs.String(), "audit.login", map[string]any{
		"outcome": "success",
		"email":   loginEmail,
	}) {
		t.Errorf("no audit.login success log line found.\nFull log buffer:\n%s", srv.Logs.String())
	}
}

// TestLoginWrongPasswordRejected is Issue 04 cycle 6: a login with wrong
// credentials returns 401, increments the per-account failed-login counter,
// and is audit-logged. An unknown email is rejected the same way.
func TestLoginWrongPasswordRejected(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	const email = "operator@acmecorp.test"
	const password = "correct-horse-battery-staple"
	if code := doFirstRun(t, srv.URL, email, password,
		"00000000-0000-0000-0000-0000000000f1"); code != http.StatusCreated {
		t.Fatalf("first-run setup: got %d want 201", code)
	}

	failedCount := func() int {
		t.Helper()
		var n int
		if err := srv.Pool.QueryRow(ctx,
			`SELECT failed_login_count FROM operators WHERE email = $1`, email,
		).Scan(&n); err != nil {
			t.Fatalf("query failed_login_count: %v", err)
		}
		return n
	}

	// First wrong attempt.
	resp := doLogin(t, srv.URL, email, "wrong-password", "00000000-0000-0000-0000-000000000601")
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("wrong password: got %d want 401; body=%s", resp.StatusCode, raw)
	}
	resp.Body.Close()
	if got := failedCount(); got != 1 {
		t.Errorf("failed_login_count after one bad attempt: got %d want 1", got)
	}

	// Second wrong attempt — counter accumulates.
	resp = doLogin(t, srv.URL, email, "still-wrong", "00000000-0000-0000-0000-000000000602")
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("second wrong password: got %d want 401", resp.StatusCode)
	}
	resp.Body.Close()
	if got := failedCount(); got != 2 {
		t.Errorf("failed_login_count after two bad attempts: got %d want 2", got)
	}

	// An unknown email is rejected identically — and creates no row to count.
	resp = doLogin(t, srv.URL, "ghost@acmecorp.test", "whatever", "00000000-0000-0000-0000-000000000603")
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("unknown email: got %d want 401", resp.StatusCode)
	}
	resp.Body.Close()

	if !auditLogged(srv.Logs.String(), "audit.login", map[string]any{
		"outcome": "failure",
		"reason":  "invalid_credentials",
		"email":   email,
	}) {
		t.Errorf("no audit.login failure log line found.\nFull log buffer:\n%s", srv.Logs.String())
	}
}

// TestLoginLockout is Issue 04 cycle 7: five consecutive failures lock the
// account for 15 minutes — even the correct password is then refused with
// 429 — and the lock releases once the window elapses. A fake clock drives
// window expiry.
func TestLoginLockout(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	clock := newFakeClock(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	srv := newTestServerCfg(t, ctx, authn.Config{Now: clock.Now})

	const email = "operator@acmecorp.test"
	const password = "correct-horse-battery-staple"
	if code := doFirstRun(t, srv.URL, email, password,
		"00000000-0000-0000-0000-0000000000f1"); code != http.StatusCreated {
		t.Fatalf("first-run setup: got %d want 201", code)
	}

	// Five wrong-password attempts. Each is a bad credential (401); the
	// fifth also trips the lockout.
	for i := 1; i <= 5; i++ {
		resp := doLogin(t, srv.URL, email, "wrong-password",
			fmt.Sprintf("00000000-0000-0000-0000-0000000007%02d", i))
		if resp.StatusCode != http.StatusUnauthorized {
			resp.Body.Close()
			t.Fatalf("failed attempt %d: got %d want 401", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// The account is now locked: the *correct* password is refused with 429.
	resp := doLogin(t, srv.URL, email, password, "00000000-0000-0000-0000-000000000710")
	if resp.StatusCode != http.StatusTooManyRequests {
		resp.Body.Close()
		t.Fatalf("locked account, correct password: got %d want 429", resp.StatusCode)
	}
	resp.Body.Close()

	var lockedUntil *time.Time
	if err := srv.Pool.QueryRow(ctx,
		`SELECT locked_until FROM operators WHERE email = $1`, email,
	).Scan(&lockedUntil); err != nil {
		t.Fatalf("query locked_until: %v", err)
	}
	if lockedUntil == nil || !lockedUntil.After(clock.Now()) {
		t.Fatalf("locked_until not set in the future: %v", lockedUntil)
	}

	// 14 minutes in, still inside the window — still locked.
	clock.Advance(14 * time.Minute)
	resp = doLogin(t, srv.URL, email, password, "00000000-0000-0000-0000-000000000711")
	if resp.StatusCode != http.StatusTooManyRequests {
		resp.Body.Close()
		t.Fatalf("14 min into lockout: got %d want 429", resp.StatusCode)
	}
	resp.Body.Close()

	// Past the 15-minute window, the correct password works again.
	clock.Advance(2 * time.Minute)
	resp = doLogin(t, srv.URL, email, password, "00000000-0000-0000-0000-000000000712")
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("after lockout window: got %d want 200; body=%s", resp.StatusCode, raw)
	}
	resp.Body.Close()

	// The recovered login cleared the lockout state.
	var failedCount int
	var clearedLock *time.Time
	if err := srv.Pool.QueryRow(ctx,
		`SELECT failed_login_count, locked_until FROM operators WHERE email = $1`, email,
	).Scan(&failedCount, &clearedLock); err != nil {
		t.Fatalf("query lockout state: %v", err)
	}
	if failedCount != 0 {
		t.Errorf("failed_login_count after recovery: got %d want 0", failedCount)
	}
	if clearedLock != nil {
		t.Errorf("locked_until not cleared after successful login: %v", clearedLock)
	}

	if !auditLogged(srv.Logs.String(), "audit.login", map[string]any{
		"outcome": "failure",
		"reason":  "account_locked",
		"email":   email,
	}) {
		t.Errorf("no audit.login account_locked log line found.\nFull log buffer:\n%s", srv.Logs.String())
	}
}

// fakeClock is a manually-advanced clock for lockout-window tests. Its Now
// method is passed to authn.Config.Now; Advance moves it forward.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// doLogin POSTs /auth/login. The caller owns resp.Body.
func doLogin(t *testing.T, baseURL, email, password, idempotencyKey string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]any{"email": email, "password": password})
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

// auditLogged reports whether the captured slog buffer contains a JSON line
// whose msg equals wantMsg and whose fields all match want.
func auditLogged(buf, wantMsg string, want map[string]any) bool {
	for line := range strings.SplitSeq(buf, "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["msg"] != wantMsg {
			continue
		}
		match := true
		for k, v := range want {
			if entry[k] != v {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
