package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
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
