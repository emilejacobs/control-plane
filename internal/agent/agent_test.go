package agent_test

import (
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
	"github.com/emilejacobs/control-plane/internal/service"
)

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
