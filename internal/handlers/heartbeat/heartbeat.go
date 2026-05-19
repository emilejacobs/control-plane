package heartbeat

import (
	"context"
	"encoding/json"
	"runtime"
	"time"
)

type Response struct {
	DeviceID      string `json:"device_id"`
	Version       string `json:"version"`
	OS            string `json:"os"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

type Handler struct {
	deviceID  string
	version   string
	startTime time.Time
}

func New(deviceID, version string, startTime time.Time) *Handler {
	return &Handler{deviceID: deviceID, version: version, startTime: startTime}
}

func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	return Response{
		DeviceID:      h.deviceID,
		Version:       h.version,
		OS:            runtime.GOOS,
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
	}, nil
}
