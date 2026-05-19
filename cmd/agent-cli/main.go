package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/uknomi/control-plane/internal/envelope"
	"github.com/uknomi/control-plane/internal/transport"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	broker := flag.String("broker", "", "broker URL, e.g. tls://example.com:8883")
	caCert := flag.String("ca", "", "path to CA cert PEM")
	cert := flag.String("cert", "", "path to client cert PEM")
	key := flag.String("key", "", "path to client key PEM")
	device := flag.String("device", "", "target device ID")
	cmdType := flag.String("command", "heartbeat", "command type to send")
	argsJSON := flag.String("args", "", "JSON for command args (optional)")
	timeout := flag.Duration("timeout", 5*time.Second, "response timeout")
	flag.Parse()

	required := map[string]string{"broker": *broker, "ca": *caCert, "cert": *cert, "key": *key, "device": *device}
	for name, val := range required {
		if val == "" {
			logger.Error("missing required flag", "flag", name)
			os.Exit(2)
		}
	}

	caPEM, err := os.ReadFile(*caCert)
	if err != nil {
		logger.Error("read ca", "error", err)
		os.Exit(1)
	}
	certPEM, err := os.ReadFile(*cert)
	if err != nil {
		logger.Error("read cert", "error", err)
		os.Exit(1)
	}
	keyPEM, err := os.ReadFile(*key)
	if err != nil {
		logger.Error("read key", "error", err)
		os.Exit(1)
	}

	tr, err := transport.New(transport.Config{
		BrokerURL: *broker,
		ClientID:  "agent-cli-" + newID()[:8],
		CACertPEM: caPEM,
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
	})
	if err != nil {
		logger.Error("transport", "error", err)
		os.Exit(1)
	}
	defer tr.Close()

	correlationID := newID()
	cmd := envelope.Command{
		CorrelationID: correlationID,
		CommandID:     newID(),
		Type:          *cmdType,
		IssuedAt:      time.Now(),
	}
	if *argsJSON != "" {
		cmd.Args = json.RawMessage(*argsJSON)
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		logger.Error("marshal command", "error", err)
		os.Exit(1)
	}

	resultCh := make(chan []byte, 1)
	resultTopic := fmt.Sprintf("devices/%s/cmd-result", *device)
	if err := tr.Subscribe(resultTopic, func(_ string, payload []byte) {
		var r envelope.Result
		if err := json.Unmarshal(payload, &r); err != nil {
			return
		}
		if r.CorrelationID == correlationID {
			select {
			case resultCh <- payload:
			default:
			}
		}
	}); err != nil {
		logger.Error("subscribe", "error", err)
		os.Exit(1)
	}

	// Brief pause for the broker to register the subscription before publishing.
	time.Sleep(100 * time.Millisecond)

	cmdTopic := fmt.Sprintf("devices/%s/cmd", *device)
	if err := tr.Publish(cmdTopic, cmdBytes); err != nil {
		logger.Error("publish", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	select {
	case raw := <-resultCh:
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			fmt.Println(string(raw))
		} else {
			fmt.Println(pretty.String())
		}
	case <-ctx.Done():
		logger.Error("timeout waiting for result", "correlation_id", correlationID, "timeout", *timeout)
		os.Exit(1)
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
