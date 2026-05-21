package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"time"
)

// Transport is the minimal subset of the agent transport the publisher needs.
type Transport interface {
	Publish(topic string, payload []byte) error
}

// Publisher periodically runs a set of collectors and publishes their merged
// output as a single JSON payload on devices/{id}/telemetry. Each tick gets a
// fresh correlation_id; a heartbeat is its own correlation, not tied to any
// inbound command.
type Publisher struct {
	Interval   time.Duration
	DeviceID   string
	Collectors []func() map[string]any
	Transport  Transport
	Logger     *slog.Logger
}

// Run blocks until ctx is cancelled, publishing on every Interval tick.
func (p *Publisher) Run(ctx context.Context) {
	log := p.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.publishOnce(log)
		}
	}
}

func (p *Publisher) publishOnce(log *slog.Logger) {
	correlationID := newCorrelationID()
	payload := map[string]any{"correlation_id": correlationID}

	for i, c := range p.Collectors {
		out := runCollector(log, i, correlationID, c)
		for k, v := range out {
			payload[k] = v
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Error("telemetry marshal failed", "error", err, "correlation_id", correlationID)
		return
	}
	topic := "devices/" + p.DeviceID + "/telemetry"
	if err := p.Transport.Publish(topic, body); err != nil {
		log.Error("telemetry publish failed", "error", err, "correlation_id", correlationID, "topic", topic)
	}
}

func runCollector(log *slog.Logger, index int, correlationID string, c func() map[string]any) (out map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("telemetry collector panicked",
				"panic", r,
				"collector_index", index,
				"correlation_id", correlationID,
			)
			out = nil
		}
	}()
	return c()
}

func newCorrelationID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
