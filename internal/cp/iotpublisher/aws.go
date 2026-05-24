// Package iotpublisher wraps the AWS IoT Data Plane Publish API so
// the cp-api PUT /devices/{id}/service-config handler can push
// config.update down to a device's MQTT cmd topic (Phase 2 slice 2).
//
// Scope is the publish primitive only — neither subscription nor
// presence is needed; cmd-result flow into cp-ingest via an IoT Rule
// → SQS pipeline (mirrors the heartbeat ingest module).
package iotpublisher

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotdataplane"
)

// AWS wraps an iotdataplane.Client. Publishes at QoS 1 (at-least-once)
// so a transient broker-side issue doesn't silently drop a config.update.
// Idempotency at the agent side is provided by the cmd dispatcher +
// the Applier's atomic write — a re-delivery is a no-op on identical
// payload.
type AWS struct {
	client *iotdataplane.Client
}

func NewAWS(client *iotdataplane.Client) *AWS {
	return &AWS{client: client}
}

func (p *AWS) Publish(ctx context.Context, topic string, payload []byte) error {
	_, err := p.client.Publish(ctx, &iotdataplane.PublishInput{
		Topic:   aws.String(topic),
		Qos:     1,
		Payload: payload,
	})
	if err != nil {
		return fmt.Errorf("iot publish %s: %w", topic, err)
	}
	return nil
}
