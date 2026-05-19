package transport_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type testCerts struct {
	CAPEM         []byte
	ServerCertPEM []byte
	ServerKeyPEM  []byte
	ClientCertPEM []byte
	ClientKeyPEM  []byte
}

func generateTestCerts(t *testing.T) testCerts {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	must(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "uknomi-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	must(t, err)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	must(t, err)
	serverTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTmpl, caTmpl, &serverKey.PublicKey, caKey)
	must(t, err)
	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)})

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	must(t, err)
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caTmpl, &clientKey.PublicKey, caKey)
	must(t, err)
	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)})

	return testCerts{
		CAPEM:         caPEM,
		ServerCertPEM: serverCertPEM,
		ServerKeyPEM:  serverKeyPEM,
		ClientCertPEM: clientCertPEM,
		ClientKeyPEM:  clientKeyPEM,
	}
}

const mosquittoConf = `listener 8883
protocol mqtt
cafile /mosquitto/config/ca.crt
certfile /mosquitto/config/server.crt
keyfile /mosquitto/config/server.key
require_certificate true
allow_anonymous true
`

type mosquittoFixture struct {
	BrokerURL string
	container testcontainers.Container
	ctx       context.Context
}

func startMosquitto(t *testing.T, ctx context.Context, certs testCerts) *mosquittoFixture {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "eclipse-mosquitto:2",
		ExposedPorts: []string{"8883/tcp"},
		WaitingFor:   wait.ForListeningPort("8883/tcp").WithStartupTimeout(30 * time.Second),
		Files: []testcontainers.ContainerFile{
			{ContainerFilePath: "/mosquitto/config/mosquitto.conf", Reader: strings.NewReader(mosquittoConf), FileMode: 0o644},
			{ContainerFilePath: "/mosquitto/config/ca.crt", Reader: bytes.NewReader(certs.CAPEM), FileMode: 0o644},
			{ContainerFilePath: "/mosquitto/config/server.crt", Reader: bytes.NewReader(certs.ServerCertPEM), FileMode: 0o644},
			{ContainerFilePath: "/mosquitto/config/server.key", Reader: bytes.NewReader(certs.ServerKeyPEM), FileMode: 0o644},
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start mosquitto: %v", err)
	}

	host, err := container.Host(ctx)
	must(t, err)
	port, err := container.MappedPort(ctx, "8883/tcp")
	must(t, err)

	fixture := &mosquittoFixture{
		BrokerURL: fmt.Sprintf("tls://%s:%s", host, port.Port()),
		container: container,
		ctx:       ctx,
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	return fixture
}

func (f *mosquittoFixture) Restart(t *testing.T) {
	t.Helper()
	timeout := 5 * time.Second
	if err := f.container.Stop(f.ctx, &timeout); err != nil {
		t.Fatalf("stop mosquitto: %v", err)
	}
	if err := f.container.Start(f.ctx); err != nil {
		t.Fatalf("start mosquitto: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}
