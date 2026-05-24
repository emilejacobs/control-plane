package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/agent"
)

// recordingCollector + recordingPublisher capture hot-reload calls so
// the Applier test can verify "nil pointer == do not touch" and "non-nil
// pointer == hot-reload fires with the new value".
type recordingCollector struct {
	mu       sync.Mutex
	listSets [][]string
}

func (r *recordingCollector) SetAllowList(list []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listSets = append(r.listSets, list)
}

type recordingPublisher struct {
	mu           sync.Mutex
	intervalSets []time.Duration
}

func (r *recordingPublisher) SetInterval(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.intervalSets = append(r.intervalSets, d)
}

const seedConfig = `{
  "device_id": "dev-test",
  "version": "abc123",
  "broker_url": "wss://example.test",
  "client_id": "dev-test",
  "cert_path": "/var/uknomi/cert.pem",
  "key_path": "/var/uknomi/key.pem",
  "ca_cert_path": "/var/uknomi/ca.pem",
  "telemetry_interval": "30s",
  "service_allow_list": ["com.uknomi.webui", "com.tailscale.tailscaled"],
  "service_status_interval": "5m"
}
`

// Happy path: setting both fields overwrites the on-disk JSON AND
// triggers SetAllowList / SetInterval on the collector + publisher.
// Effective values are returned for the cmd-result ACK payload.
func TestApplierSetsBoth(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent-config.json")
	if err := os.WriteFile(cfgPath, []byte(seedConfig), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	col, pub := &recordingCollector{}, &recordingPublisher{}
	app := agent.NewConfigUpdateApplier(cfgPath, col, pub)

	list := []string{"a", "b", "c"}
	interval := 2 * time.Minute
	gotList, gotInt, err := app.Apply(context.Background(), &list, &interval)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(gotList) != 3 || gotList[0] != "a" {
		t.Errorf("effective list: got %v, want [a b c]", gotList)
	}
	if gotInt != 2*time.Minute {
		t.Errorf("effective interval: got %v, want 2m", gotInt)
	}

	if len(col.listSets) != 1 || col.listSets[0][0] != "a" || len(col.listSets[0]) != 3 {
		t.Errorf("collector.SetAllowList: got %v, want one call with [a b c]", col.listSets)
	}
	if len(pub.intervalSets) != 1 || pub.intervalSets[0] != 2*time.Minute {
		t.Errorf("publisher.SetInterval: got %v, want one call with 2m", pub.intervalSets)
	}

	onDisk := readConfigMap(t, cfgPath)
	if got := onDisk["service_status_interval"]; got != "2m" && got != "2m0s" {
		t.Errorf("on-disk interval: got %q, want 2m or 2m0s", got)
	}
	listOnDisk, _ := onDisk["service_allow_list"].([]any)
	if len(listOnDisk) != 3 || listOnDisk[0] != "a" {
		t.Errorf("on-disk list: got %v, want [a b c]", listOnDisk)
	}
	// Preserved fields the Applier must not clobber.
	if onDisk["broker_url"] != "wss://example.test" {
		t.Errorf("broker_url clobbered: got %v", onDisk["broker_url"])
	}
	if onDisk["device_id"] != "dev-test" {
		t.Errorf("device_id clobbered: got %v", onDisk["device_id"])
	}
}

// nil for either field means "leave it alone" — on disk AND in the
// publisher. Per PRD § "Override clearing" — slice 2 treats nil as
// "don't touch", not "revert to install default". That keeps the agent
// stateless about the install-time defaults.
func TestApplierNilLeavesFieldUntouched(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent-config.json")
	if err := os.WriteFile(cfgPath, []byte(seedConfig), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	col, pub := &recordingCollector{}, &recordingPublisher{}
	app := agent.NewConfigUpdateApplier(cfgPath, col, pub)

	// Set list, leave interval nil.
	list := []string{"x"}
	gotList, gotInt, err := app.Apply(context.Background(), &list, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(gotList) != 1 || gotList[0] != "x" {
		t.Errorf("effective list: got %v, want [x]", gotList)
	}
	if gotInt != 5*time.Minute {
		t.Errorf("effective interval: got %v, want preserved 5m", gotInt)
	}
	if len(pub.intervalSets) != 0 {
		t.Errorf("publisher.SetInterval should not fire for nil interval; got %v", pub.intervalSets)
	}

	onDisk := readConfigMap(t, cfgPath)
	if onDisk["service_status_interval"] != "5m" {
		t.Errorf("on-disk interval: got %q, want preserved 5m", onDisk["service_status_interval"])
	}

	// Now set interval, leave list nil. Use a fresh applier to reset
	// the call counts on the recorders.
	col2, pub2 := &recordingCollector{}, &recordingPublisher{}
	app2 := agent.NewConfigUpdateApplier(cfgPath, col2, pub2)
	interval := 90 * time.Second
	gotList2, gotInt2, err := app2.Apply(context.Background(), nil, &interval)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(gotList2) != 1 || gotList2[0] != "x" {
		t.Errorf("effective list: got %v, want preserved [x]", gotList2)
	}
	if gotInt2 != 90*time.Second {
		t.Errorf("effective interval: got %v, want 1m30s", gotInt2)
	}
	if len(col2.listSets) != 0 {
		t.Errorf("collector.SetAllowList should not fire for nil list; got %v", col2.listSets)
	}
}

// Explicit [] is a meaningful override ("track nothing") — distinct
// from nil. Disk gets [], collector is told to use [].
func TestApplierEmptyListAppliedNotIgnored(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent-config.json")
	if err := os.WriteFile(cfgPath, []byte(seedConfig), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	col, pub := &recordingCollector{}, &recordingPublisher{}
	app := agent.NewConfigUpdateApplier(cfgPath, col, pub)

	empty := []string{}
	gotList, _, err := app.Apply(context.Background(), &empty, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if gotList == nil || len(gotList) != 0 {
		t.Errorf("effective list: got %v, want non-nil empty []", gotList)
	}
	if len(col.listSets) != 1 || len(col.listSets[0]) != 0 {
		t.Errorf("collector.SetAllowList: got %v, want one call with []", col.listSets)
	}

	onDisk := readConfigMap(t, cfgPath)
	listOnDisk, ok := onDisk["service_allow_list"].([]any)
	if !ok {
		t.Fatalf("on-disk list type: got %T, want []any", onDisk["service_allow_list"])
	}
	if len(listOnDisk) != 0 {
		t.Errorf("on-disk list: got %v, want []", listOnDisk)
	}
}

// Atomic write: a mid-write crash must not corrupt agent-config.json.
// The Applier writes to a .tmp sibling and renames over the target,
// per PRD § Persistence ordering. The test checks the *outcome*: no
// .tmp file lingers after a successful Apply, and the file mode is
// preserved.
func TestApplierAtomicWriteCleansUp(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent-config.json")
	if err := os.WriteFile(cfgPath, []byte(seedConfig), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	col, pub := &recordingCollector{}, &recordingPublisher{}
	app := agent.NewConfigUpdateApplier(cfgPath, col, pub)

	list := []string{"a"}
	if _, _, err := app.Apply(context.Background(), &list, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
	stat, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Errorf("file mode: got %v, want 0600", stat.Mode().Perm())
	}
}

func readConfigMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return m
}
