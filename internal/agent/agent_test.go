package agent_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/agent"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/handlers/networkscan"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
	protologtail "github.com/emilejacobs/control-plane/internal/protocol/logtail"
	protonetworkscan "github.com/emilejacobs/control-plane/internal/protocol/networkscan"
	"github.com/emilejacobs/control-plane/internal/service"
)

// stubProbeBackend implements probes.Backend with a fixed result set.
type stubProbeBackend struct{ results []healthprobes.Result }

func (s stubProbeBackend) Collect(_ context.Context) []healthprobes.Result { return s.results }

func TestAgentPublishesHealthProbes(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()

	backend := stubProbeBackend{results: []healthprobes.Result{
		{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusGreen, State: "configured"},
	}}
	a, err := agent.New(agent.Config{
		CertPath:      cert,
		DeviceID:      "dev-001",
		Version:       "0.1.0",
		ProbeInterval: 5 * time.Millisecond,
	}, tr, agent.WithProbeBackend(backend))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	topic := "devices/dev-001/health-probes"
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(tr.publishedOn(topic)) > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	publishes := tr.publishedOn(topic)
	if len(publishes) == 0 {
		t.Fatalf("no health-probes publish within 1s on %s", topic)
	}
	var report healthprobes.Report
	if err := json.Unmarshal(publishes[0], &report); err != nil {
		t.Fatalf("payload not a valid Report: %v", err)
	}
	if report.DeviceID != "dev-001" || len(report.Probes) != 1 ||
		report.Probes[0].Name != healthprobes.ProbeAutoLogin {
		t.Errorf("unexpected report: %+v", report)
	}
}

type fakeTransport struct {
	mu        sync.Mutex
	subs      map[string]func(string, []byte)
	published map[string][][]byte
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		subs:      make(map[string]func(string, []byte)),
		published: make(map[string][][]byte),
	}
}

func (f *fakeTransport) Subscribe(topic string, h func(string, []byte)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[topic] = h
	return nil
}

func (f *fakeTransport) Publish(topic string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published[topic] = append(f.published[topic], payload)
	return nil
}

func (f *fakeTransport) Close() error { return nil }

func (f *fakeTransport) deliverTo(topic string, payload []byte) {
	f.mu.Lock()
	h := f.subs[topic]
	f.mu.Unlock()
	if h != nil {
		h(topic, payload)
	}
}

func (f *fakeTransport) publishedOn(topic string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.published[topic]))
	copy(out, f.published[topic])
	return out
}

// wedgedTransport is a fakeTransport with a controllable LastPublishSuccess,
// simulating a dead MQTT session whose publishes never succeed (#65). Because it
// implements the liveness reporter, the agent wires a watchdog around it. last
// is mutex-guarded — the watchdog reads it from its own goroutine.
type wedgedTransport struct {
	*fakeTransport
	mu   sync.Mutex
	last time.Time
}

func newWedgedTransport(last time.Time) *wedgedTransport {
	return &wedgedTransport{fakeTransport: newFakeTransport(), last: last}
}

func (w *wedgedTransport) LastPublishSuccess() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

func (w *wedgedTransport) setLast(t time.Time) {
	w.mu.Lock()
	w.last = t
	w.mu.Unlock()
}

// A session whose last successful publish is far in the past is detected as
// wedged: the agent signals WedgeDetected so main can exit and let launchd
// restart it with a fresh transport (#65, exit-based recovery).
func TestAgentWatchdogSignalsWedgedSession(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newWedgedTransport(time.Now().Add(-time.Hour))

	a, err := agent.New(agent.Config{
		CertPath:           cert,
		DeviceID:           "dev-wedge",
		Version:            "1.0.0",
		WatchdogStaleAfter: 20 * time.Millisecond,
		WatchdogInterval:   time.Millisecond,
	}, tr)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	select {
	case <-a.WedgeDetected():
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not signal a wedged session")
	}
}

// A healthy transport (LastPublishSuccess keeps advancing) is never flagged,
// even over many watchdog checks.
func TestAgentWatchdogQuietWhenHealthy(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newWedgedTransport(time.Now())

	a, err := agent.New(agent.Config{
		CertPath:           cert,
		DeviceID:           "dev-healthy",
		Version:            "1.0.0",
		WatchdogStaleAfter: 20 * time.Millisecond,
		WatchdogInterval:   time.Millisecond,
	}, tr)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Keep the liveness signal fresh across many check intervals.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			tr.setLast(time.Now())
			time.Sleep(time.Millisecond)
		}
	}()
	<-done

	select {
	case <-a.WedgeDetected():
		t.Fatal("watchdog flagged a healthy session")
	default:
	}
}

func writeTestCert(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return path
}

func TestAgentNewRefusesWithMissingCert(t *testing.T) {
	_, err := agent.New(agent.Config{
		CertPath: "/nonexistent/cert.pem",
	}, newFakeTransport())

	if err == nil {
		t.Fatal("expected error for missing cert, got nil")
	}
	if !strings.Contains(err.Error(), "/nonexistent/cert.pem") {
		t.Errorf("error should name the missing cert path; got: %v", err)
	}
}

func TestAgentNewRefusesWithMalformedCert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(path, []byte("this is not a PEM-encoded certificate"), 0o600); err != nil {
		t.Fatalf("write bad cert: %v", err)
	}

	_, err := agent.New(agent.Config{CertPath: path}, newFakeTransport())
	if err == nil {
		t.Fatal("expected error for malformed cert, got nil")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the bad cert path; got: %v", err)
	}
}

func TestAgentNewRefusesWithExpiredCert(t *testing.T) {
	expired := writeTestCert(t, time.Now().Add(-time.Hour))

	_, err := agent.New(agent.Config{CertPath: expired}, newFakeTransport())
	if err == nil {
		t.Fatal("expected error for expired cert, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "expir") {
		t.Errorf("error should mention expiry; got: %v", err)
	}
}

func TestAgentNewAcceptsValidCert(t *testing.T) {
	valid := writeTestCert(t, time.Now().Add(time.Hour))

	_, err := agent.New(agent.Config{CertPath: valid}, newFakeTransport())
	if err != nil {
		t.Fatalf("expected no error for valid cert, got: %v", err)
	}
}

func TestAgentDispatchesCommandsAndPublishesResults(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()

	a, err := agent.New(agent.Config{
		CertPath: cert,
		DeviceID: "dev-001",
		Version:  "0.1.0",
	}, tr)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{
		Type:          "heartbeat",
		CorrelationID: "c1",
		CommandID:     "x1",
		IssuedAt:      time.Now(),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal cmd: %v", err)
	}

	tr.deliverTo("devices/dev-001/cmd", cmdBytes)

	results := tr.publishedOn("devices/dev-001/cmd-result")
	if len(results) != 1 {
		t.Fatalf("expected 1 result published, got %d", len(results))
	}

	var result envelope.Result
	if err := json.Unmarshal(results[0], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %+v", result)
	}
	if result.CorrelationID != "c1" {
		t.Errorf("CorrelationID: got %q, want c1", result.CorrelationID)
	}

	var hb map[string]any
	if err := json.Unmarshal(result.Result, &hb); err != nil {
		t.Fatalf("unmarshal heartbeat payload: %v", err)
	}
	if hb["device_id"] != "dev-001" {
		t.Errorf("result.device_id: got %v, want dev-001", hb["device_id"])
	}
}

func TestAgentDispatchesServiceStatus(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()
	backend := &service.Fake{
		States: map[string]service.State{"nginx": service.StateRunning},
	}

	a, err := agent.New(agent.Config{
		CertPath: cert,
		DeviceID: "dev-001",
		Version:  "0.1.0",
	}, tr, agent.WithServiceBackend(backend))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{
		Type:          "service.status",
		CorrelationID: "c1",
		CommandID:     "x1",
		Args:          json.RawMessage(`{"name": "nginx"}`),
		IssuedAt:      time.Now(),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal cmd: %v", err)
	}

	tr.deliverTo("devices/dev-001/cmd", cmdBytes)

	results := tr.publishedOn("devices/dev-001/cmd-result")
	if len(results) != 1 {
		t.Fatalf("expected 1 result published, got %d", len(results))
	}
	var result envelope.Result
	if err := json.Unmarshal(results[0], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %+v", result)
	}
	var payload map[string]string
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["state"] != "running" {
		t.Errorf("state: got %q, want running", payload["state"])
	}
	if payload["name"] != "nginx" {
		t.Errorf("name: got %q, want nginx", payload["name"])
	}
}

// Phase 2 slice 2: the agent registers a config.update handler when
// constructed with a ConfigPath. A delivered config.update payload
// flows through the dispatcher, persists to disk, and ACKs on
// cmd-result with the effective values.
func TestAgentDispatchesConfigUpdate(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent-config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"device_id": "dev-cfg",
		"version": "0.1.0",
		"broker_url": "wss://example.test",
		"client_id": "dev-cfg",
		"cert_path": "/var/uknomi/cert.pem",
		"key_path": "/var/uknomi/key.pem",
		"ca_cert_path": "/var/uknomi/ca.pem",
		"service_allow_list": ["nginx"],
		"service_status_interval": "5m"
	}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	tr := newFakeTransport()
	backend := &service.Fake{States: map[string]service.State{"nginx": service.StateRunning, "anydesk": service.StateRunning}}
	a, err := agent.New(agent.Config{
		CertPath:         cert,
		DeviceID:         "dev-cfg",
		Version:          "0.1.0",
		ConfigPath:       cfgPath,
		ServiceAllowList: []string{"nginx"},
	}, tr, agent.WithServiceBackend(backend))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{
		Type:          "config.update",
		CorrelationID: "corr-99",
		CommandID:     "cmd-99",
		Args:          json.RawMessage(`{"service_allow_list":["nginx","anydesk"],"service_status_interval":"45s"}`),
		IssuedAt:      time.Now(),
	}
	cmdBytes, _ := json.Marshal(cmd)
	tr.deliverTo("devices/dev-cfg/cmd", cmdBytes)

	results := tr.publishedOn("devices/dev-cfg/cmd-result")
	if len(results) != 1 {
		t.Fatalf("expected 1 cmd-result, got %d", len(results))
	}
	var result envelope.Result
	if err := json.Unmarshal(results[0], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %+v error=%+v", result, result.Error)
	}
	if result.CorrelationID != "corr-99" {
		t.Errorf("correlation_id round-trip: got %q, want corr-99", result.CorrelationID)
	}

	// Effective values returned to the cp.
	var payload struct {
		EffectiveAllowList []string `json:"effective_allow_list"`
		EffectiveInterval  string   `json:"effective_interval"`
	}
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.EffectiveAllowList) != 2 || payload.EffectiveAllowList[1] != "anydesk" {
		t.Errorf("effective_allow_list: got %v", payload.EffectiveAllowList)
	}
	if payload.EffectiveInterval != "45s" {
		t.Errorf("effective_interval: got %q, want 45s", payload.EffectiveInterval)
	}

	// On-disk persistence.
	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), "anydesk") {
		t.Errorf("agent-config.json should contain new service; got: %s", raw)
	}
	if !strings.Contains(string(raw), "45s") {
		t.Errorf("agent-config.json should contain new interval; got: %s", raw)
	}
}

// Phase 2 slice 3: log.tail cmd round-trips through the dispatcher
// into the wired Reader, returns the Response in the cmd-result
// envelope with Type=="log.tail" so cp-ingest can route the ACK.
func TestAgentDispatchesLogTail(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))

	// Stub reader returns canned content for a known log_name.
	reader := &stubLogTailReader{
		allow: map[string]protologtail.Entry{
			"agent": {Name: "agent", Kind: protologtail.KindFile, Target: "/var/log/uknomi-agent.log", Label: "uknomi-agent (stdout)"},
		},
		resp: logtailResponse{content: "line 1\nline 2\n"},
	}

	tr := newFakeTransport()
	a, err := agent.New(agent.Config{
		CertPath: cert,
		DeviceID: "dev-lt",
		Version:  "099dd7f",
	}, tr, agent.WithLogTailReader(reader))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{
		Type:          "log.tail",
		CorrelationID: "corr-lt-1",
		CommandID:     "cmd-lt-1",
		Args:          json.RawMessage(`{"log_name":"agent","lines":100}`),
		IssuedAt:      time.Now(),
	}
	cmdBytes, _ := json.Marshal(cmd)
	tr.deliverTo("devices/dev-lt/cmd", cmdBytes)

	results := tr.publishedOn("devices/dev-lt/cmd-result")
	if len(results) != 1 {
		t.Fatalf("expected 1 cmd-result, got %d", len(results))
	}
	var result envelope.Result
	if err := json.Unmarshal(results[0], &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %+v err=%+v", result, result.Error)
	}
	if result.Type != "log.tail" {
		t.Errorf("Type: got %q, want log.tail", result.Type)
	}
	if result.CorrelationID != "corr-lt-1" {
		t.Errorf("correlation_id: got %q", result.CorrelationID)
	}
	var payload struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(result.Result, &payload)
	if payload.Content != "line 1\nline 2\n" {
		t.Errorf("content: got %q", payload.Content)
	}

	// Verify the reader was called with the right entry.
	if len(reader.calls) != 1 || reader.calls[0].entry.Target != "/var/log/uknomi-agent.log" || reader.calls[0].lines != 100 {
		t.Errorf("reader calls: got %+v", reader.calls)
	}
	if reader.calls[0].entry.Kind != protologtail.KindFile {
		t.Errorf("reader.calls[0].entry.Kind: got %q, want %q", reader.calls[0].entry.Kind, protologtail.KindFile)
	}
}

// stubLogTailReader implements the logtail.Reader interface for the
// agent dispatch test above.
type stubLogTailReader struct {
	allow map[string]protologtail.Entry
	calls []logtailReadCall
	resp  logtailResponse
	err   error
}

type logtailReadCall struct {
	entry protologtail.Entry
	lines int
}

type logtailResponse struct {
	content string
}

func (s *stubLogTailReader) AllowList() map[string]protologtail.Entry { return s.allow }
func (s *stubLogTailReader) Tail(entry protologtail.Entry, lines int) (protologtail.Response, error) {
	s.calls = append(s.calls, logtailReadCall{entry: entry, lines: lines})
	return protologtail.Response{Content: s.resp.content}, s.err
}

// Phase 2 Edge UI rework (issue #3): network.scan cmd round-trips
// through the dispatcher into the wired Scanner; the agent emits a
// cmd-result envelope with Type=="network.scan" so cp-ingest can route
// the ACK and stamp the per-request row.
func TestAgentDispatchesNetworkScan(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))

	sc := &stubScanner{
		hosts: []networkscan.RawHost{
			{IP: "192.168.1.42", MAC: "44:19:B6:AA:BB:CC", OpenPorts: []int{80, 554, 22}},
		},
	}

	tr := newFakeTransport()
	a, err := agent.New(agent.Config{
		CertPath: cert,
		DeviceID: "dev-ns",
		Version:  "099dd7f",
	}, tr, agent.WithNetworkScanner(sc))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{
		Type:          "network.scan",
		CorrelationID: "corr-ns-1",
		CommandID:     "cmd-ns-1",
		Args:          json.RawMessage(`{"cidr":"192.168.1.0/24"}`),
		IssuedAt:      time.Now(),
	}
	cmdBytes, _ := json.Marshal(cmd)
	tr.deliverTo("devices/dev-ns/cmd", cmdBytes)

	results := tr.publishedOn("devices/dev-ns/cmd-result")
	if len(results) != 1 {
		t.Fatalf("expected 1 cmd-result, got %d", len(results))
	}
	var result envelope.Result
	if err := json.Unmarshal(results[0], &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %+v err=%+v", result, result.Error)
	}
	if result.Type != "network.scan" {
		t.Errorf("Type: got %q, want network.scan", result.Type)
	}
	var payload protonetworkscan.Response
	_ = json.Unmarshal(result.Result, &payload)
	if len(payload.Hosts) != 1 {
		t.Fatalf("hosts: got %d, want 1", len(payload.Hosts))
	}
	h := payload.Hosts[0]
	if h.IP != "192.168.1.42" || h.Vendor != "Hikvision" {
		t.Errorf("host: %+v", h)
	}
	// open_ports is filtered (22 dropped) and sorted.
	if len(h.OpenPorts) != 2 || h.OpenPorts[0] != 80 || h.OpenPorts[1] != 554 {
		t.Errorf("open_ports: got %v, want [80 554]", h.OpenPorts)
	}
	if sc.calls != 1 {
		t.Errorf("scanner.Scan calls: got %d, want 1", sc.calls)
	}
}

// stubScanner is the agent-level fake for the network.scan dispatch
// test. Returns canned hosts; counts calls so the test can pin
// dispatcher behaviour.
type stubScanner struct {
	hosts []networkscan.RawHost
	err   error
	calls int
}

func (s *stubScanner) Scan(_ context.Context, _ string) ([]networkscan.RawHost, error) {
	s.calls++
	return s.hosts, s.err
}

func TestAgentDispatchesServiceRestart(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()
	backend := &service.Fake{} // empty maps: Restart succeeds, Status returns ErrNotFound

	a, err := agent.New(agent.Config{
		CertPath: cert,
		DeviceID: "dev-001",
		Version:  "0.1.0",
	}, tr, agent.WithServiceBackend(backend))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{
		Type:          "service.restart",
		CorrelationID: "c1",
		CommandID:     "x1",
		Args:          json.RawMessage(`{"name": "nginx"}`),
		IssuedAt:      time.Now(),
	}
	cmdBytes, _ := json.Marshal(cmd)
	tr.deliverTo("devices/dev-001/cmd", cmdBytes)

	results := tr.publishedOn("devices/dev-001/cmd-result")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	var result envelope.Result
	if err := json.Unmarshal(results[0], &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["name"] != "nginx" {
		t.Errorf("name: got %v, want nginx", payload["name"])
	}
	if _, ok := payload["started_at"].(string); !ok {
		t.Errorf("started_at missing or not a string: %v", payload["started_at"])
	}
	if _, ok := payload["finished_at"].(string); !ok {
		t.Errorf("finished_at missing or not a string: %v", payload["finished_at"])
	}
}

func TestAgentPublishesTelemetryHeartbeats(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()

	a, err := agent.New(agent.Config{
		CertPath:          cert,
		DeviceID:          "dev-001",
		Version:           "0.1.0",
		TelemetryInterval: 5 * time.Millisecond,
	}, tr)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	topic := "devices/dev-001/telemetry"
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(tr.publishedOn(topic)) > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	publishes := tr.publishedOn(topic)
	if len(publishes) == 0 {
		t.Fatalf("no telemetry publish within 1s on %s", topic)
	}

	var payload map[string]any
	if err := json.Unmarshal(publishes[0], &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["device_id"] != "dev-001" {
		t.Errorf("device_id: got %v, want dev-001", payload["device_id"])
	}
	if payload["version"] != "0.1.0" {
		t.Errorf("version: got %v, want 0.1.0", payload["version"])
	}
	if _, ok := payload["os"].(string); !ok {
		t.Errorf("os: got %v, want a string", payload["os"])
	}
	if _, ok := payload["uptime_seconds"].(float64); !ok {
		t.Errorf("uptime_seconds: got %T, want number", payload["uptime_seconds"])
	}
	if v, ok := payload["last_command_at"]; !ok || v != nil {
		t.Errorf("last_command_at: want nil (no commands yet), got %v (present=%v)", v, ok)
	}
	if corr, ok := payload["correlation_id"].(string); !ok || corr == "" {
		t.Errorf("correlation_id missing or empty")
	}
}
