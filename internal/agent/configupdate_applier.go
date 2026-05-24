package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CollectorReloader is the subset of *telemetry.ServiceStatusCollector
// the ConfigUpdateApplier needs. Interface (not the concrete type) so
// tests can stub it; the production wiring passes the real collector.
type CollectorReloader interface {
	SetAllowList([]string)
}

// PublisherReloader is the subset of *telemetry.ServiceStatusPublisher
// the ConfigUpdateApplier needs.
type PublisherReloader interface {
	SetInterval(time.Duration)
}

// ConfigUpdateApplier persists a config.update payload to the agent's
// JSON config file and hot-reloads the running collector + publisher.
//
// nil for either field in Apply means "leave it alone" — neither the
// disk nor the publisher are touched. Per PRD § Override clearing
// (slice 2), the agent stays stateless about install-time defaults; if
// CP wants to "revert", it must send the original values explicitly.
//
// Disk writes are atomic: write to a .tmp sibling, fsync, os.Rename
// over the target. The on-disk mode (0600) is preserved.
//
// Read-modify-write uses map[string]any so the Applier doesn't clobber
// any fields outside its two-key whitelist (e.g. broker_url, cert
// paths). Schema additions in internal/config don't need the Applier
// to know about them.
type ConfigUpdateApplier struct {
	path      string
	collector CollectorReloader
	publisher PublisherReloader
}

func NewConfigUpdateApplier(path string, col CollectorReloader, pub PublisherReloader) *ConfigUpdateApplier {
	return &ConfigUpdateApplier{path: path, collector: col, publisher: pub}
}

// Apply persists the override(s) and returns the effective values
// (post-write). The effective values are what the cmd-result ACK
// reports back to CP so the dashboard can show "applied".
func (a *ConfigUpdateApplier) Apply(_ context.Context, allowList *[]string, interval *time.Duration) ([]string, time.Duration, error) {
	raw, err := os.ReadFile(a.path)
	if err != nil {
		return nil, 0, fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, 0, fmt.Errorf("parse config: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}

	dirty := false
	if allowList != nil {
		jsonList := make([]any, len(*allowList))
		for i, s := range *allowList {
			jsonList[i] = s
		}
		m["service_allow_list"] = jsonList
		dirty = true
	}
	if interval != nil {
		m["service_status_interval"] = interval.String()
		dirty = true
	}

	if dirty {
		if err := atomicWriteJSON(a.path, m); err != nil {
			return nil, 0, fmt.Errorf("write config: %w", err)
		}
	}

	// Hot-reload AFTER successful disk write so a write failure
	// doesn't leave the running publisher in a state the disk
	// doesn't reflect.
	if allowList != nil {
		a.collector.SetAllowList(*allowList)
	}
	if interval != nil {
		a.publisher.SetInterval(*interval)
	}

	effList := readAllowListFromMap(m)
	effInterval, err := readIntervalFromMap(m)
	if err != nil {
		return nil, 0, fmt.Errorf("read effective interval: %w", err)
	}
	return effList, effInterval, nil
}

func atomicWriteJSON(path string, m map[string]any) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Match existing 0600 perms (see module 11). Preserve via Stat.
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func readAllowListFromMap(m map[string]any) []string {
	v, ok := m["service_allow_list"]
	if !ok || v == nil {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func readIntervalFromMap(m map[string]any) (time.Duration, error) {
	v, ok := m["service_status_interval"]
	if !ok || v == nil {
		return 0, nil
	}
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("service_status_interval not a string: %T", v)
	}
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
