// Package devices serves the read-side device endpoints defined in
// PRD § API contracts (GET /devices/{id}, GET /devices).
package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type Service interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
}

type GetHandler struct {
	svc Service
}

func NewGet(svc Service) *GetHandler { return &GetHandler{svc: svc} }

type response struct {
	DeviceID           string `json:"device_id"`
	Hostname           string `json:"hostname"`
	HardwareUUID       string `json:"hardware_uuid"`
	HardwareKind       string `json:"hardware_kind"`
	OSVersion          string `json:"os_version"`
	AgentVersion       string `json:"agent_version"`
	IoTThingARN        string `json:"iot_thing_arn"`
	IsOnline           bool   `json:"is_online"`
	LastSeenAgoSeconds *int64 `json:"last_seen_ago_seconds"`
	EnrolledAt         string `json:"enrolled_at"`
}

func (h *GetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Derive presence from the raw last_seen column. A device that has
	// never reported a heartbeat is offline with a null ago-seconds.
	now := time.Now()
	var lastSeen time.Time
	var agoSeconds *int64
	if dev.LastSeen != nil {
		lastSeen = *dev.LastSeen
		s := int64(now.Sub(lastSeen).Seconds())
		agoSeconds = &s
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{
		DeviceID:           dev.ID,
		Hostname:           dev.Hostname,
		HardwareUUID:       dev.HardwareUUID,
		HardwareKind:       dev.HardwareKind,
		OSVersion:          dev.OSVersion,
		AgentVersion:       dev.AgentVersion,
		IoTThingARN:        dev.IoTThingARN,
		IsOnline:           presence.IsOnline(lastSeen, now),
		LastSeenAgoSeconds: agoSeconds,
		EnrolledAt:         dev.EnrolledAt.UTC().Format(time.RFC3339),
	})
}
