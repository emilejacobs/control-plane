package storage_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/storage"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// Component mode: DB_PASSWORD present → a DSN is built from the parts, with
// defaults filled in for the unset ones.
func TestResolveDSNComponentDefaults(t *testing.T) {
	dsn, err := storage.ResolveDSN(env(map[string]string{
		"DB_HOST":     "db.example.com",
		"DB_PASSWORD": "s3cret",
	}))
	if err != nil {
		t.Fatalf("ResolveDSN: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig(%q): %v", dsn, err)
	}
	cc := cfg.ConnConfig
	if cc.Host != "db.example.com" || cc.Port != 5432 {
		t.Errorf("host:port = %s:%d, want db.example.com:5432", cc.Host, cc.Port)
	}
	if cc.User != "uknomi_admin" || cc.Database != "uknomi_cp" || cc.Password != "s3cret" {
		t.Errorf("user/db/pw = %s/%s/%s, want uknomi_admin/uknomi_cp/s3cret", cc.User, cc.Database, cc.Password)
	}
}

// A password full of URL-reserved characters survives the round trip — the
// whole point of building the DSN in-process instead of hand-encoding it.
func TestResolveDSNEncodesSpecialPassword(t *testing.T) {
	pw := `p@ss:w/rd?#&=+ x"'`
	dsn, err := storage.ResolveDSN(env(map[string]string{
		"DB_HOST":     "h",
		"DB_PORT":     "6543",
		"DB_USER":     "u",
		"DB_NAME":     "d",
		"DB_SSLMODE":  "verify-full",
		"DB_PASSWORD": pw,
	}))
	if err != nil {
		t.Fatalf("ResolveDSN: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig(%q): %v", dsn, err)
	}
	cc := cfg.ConnConfig
	if cc.Password != pw {
		t.Errorf("password round trip = %q, want %q", cc.Password, pw)
	}
	if cc.Host != "h" || cc.Port != 6543 || cc.User != "u" || cc.Database != "d" {
		t.Errorf("parts = %s:%d %s/%s", cc.Host, cc.Port, cc.User, cc.Database)
	}
	if cc.TLSConfig == nil {
		t.Error("sslmode=verify-full should have produced a TLS config")
	}
}

// DB_DSN is honored when no component password is present (backward-compat for
// the rollout window before task defs switch over).
func TestResolveDSNFallsBackToDBDSN(t *testing.T) {
	want := "postgresql://u:p@h:5432/d?sslmode=require"
	got, err := storage.ResolveDSN(env(map[string]string{"DB_DSN": want}))
	if err != nil {
		t.Fatalf("ResolveDSN: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Component mode wins over a stale DB_DSN if both are somehow present.
func TestResolveDSNComponentBeatsDBDSN(t *testing.T) {
	dsn, err := storage.ResolveDSN(env(map[string]string{
		"DB_DSN":      "postgresql://old:stale@oldhost:5432/old?sslmode=require",
		"DB_HOST":     "newhost",
		"DB_PASSWORD": "fresh",
	}))
	if err != nil {
		t.Fatalf("ResolveDSN: %v", err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	if cfg.ConnConfig.Host != "newhost" || cfg.ConnConfig.Password != "fresh" {
		t.Errorf("expected component values, got host=%s pw=%s", cfg.ConnConfig.Host, cfg.ConnConfig.Password)
	}
}

func TestResolveDSNErrors(t *testing.T) {
	if _, err := storage.ResolveDSN(env(map[string]string{})); err == nil {
		t.Error("no config: expected an error")
	}
	if _, err := storage.ResolveDSN(env(map[string]string{"DB_PASSWORD": "p"})); err == nil {
		t.Error("DB_PASSWORD without DB_HOST: expected an error")
	}
}
