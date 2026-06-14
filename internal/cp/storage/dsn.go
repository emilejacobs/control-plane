package storage

import (
	"errors"
	"net"
	"net/url"
)

// ResolveDSN builds the Postgres connection string from the environment
// (issue #49). Preferred mode reads the password from DB_PASSWORD — injected
// straight from the RDS-managed master secret via an ECS task-def secret
// reference — and the rest from plain env, building (and URL-encoding) the DSN
// in-process. This removes the hand-synced uknomi/cp/db-dsn secret that went
// stale on every RDS master-password rotation and silently took the control
// plane down.
//
// If DB_PASSWORD is unset it falls back to a complete DB_DSN, so a binary can
// run under either the old or new task definition during rollout.
//
// getenv is injected for testability (os.Getenv in production).
func ResolveDSN(getenv func(string) string) (string, error) {
	if pw := getenv("DB_PASSWORD"); pw != "" {
		host := getenv("DB_HOST")
		if host == "" {
			return "", errors.New("DB_PASSWORD is set but DB_HOST is empty")
		}
		u := url.URL{
			Scheme: "postgresql",
			User:   url.UserPassword(orDefault(getenv("DB_USER"), "uknomi_admin"), pw),
			Host:   net.JoinHostPort(host, orDefault(getenv("DB_PORT"), "5432")),
			Path:   "/" + orDefault(getenv("DB_NAME"), "uknomi_cp"),
		}
		q := url.Values{}
		q.Set("sslmode", orDefault(getenv("DB_SSLMODE"), "require"))
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	if dsn := getenv("DB_DSN"); dsn != "" {
		return dsn, nil
	}
	return "", errors.New("no database configuration: set DB_PASSWORD (+DB_HOST) or DB_DSN")
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
