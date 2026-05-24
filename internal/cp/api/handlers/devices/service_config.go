package devices

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/configupdate"
)

// ConfigStore is the persistence side of the PUT handler. The narrow
// interface keeps the handler test against a stub; *registry.Registry
// satisfies it in production.
type ConfigStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	SetServiceConfig(ctx context.Context, deviceID string, allowList *[]string, interval *string) error
}

// CmdPublisher publishes a serialised envelope.Command to an IoT topic.
// Implementations wrap AWS IoT Data Plane.
type CmdPublisher interface {
	Publish(ctx context.Context, topic string, payload []byte) error
}

// ServiceConfigPutHandler serves PUT /devices/{id}/service-config —
// the operator-edit surface for per-device allow-list + cadence
// overrides (Phase 2 slice 2). On success it persists the override
// AND publishes a config.update cmd on devices/{id}/cmd; on a
// downstream publish failure the cp-side override is still persisted
// but the response is 502 (operator can retry; the registry write is
// idempotent on re-Set, the cmd publish is the retried action).
type ServiceConfigPutHandler struct {
	store     ConfigStore
	publisher CmdPublisher
	newCmdID  func() string
}

func NewServiceConfigPut(store ConfigStore, publisher CmdPublisher) *ServiceConfigPutHandler {
	return &ServiceConfigPutHandler{store: store, publisher: publisher, newCmdID: newRandomID}
}

func newRandomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (h *ServiceConfigPutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if _, err := h.store.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Unknown-field check first so the request shape is sound before
	// we parse the individual fields.
	if err := configupdate.RejectUnknownFields(raw); err != nil {
		writeValidationError(w, err)
		return
	}
	var req configupdate.Request
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			writeValidationError(w, err)
			return
		}
	}
	allowList, err := configupdate.ParseAllowList(req.ServiceAllowList)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	// Validate the interval against the same rules the agent enforces,
	// but preserve the operator's original string form (so "5m" stays
	// "5m" instead of becoming "5m0s") all the way to disk + cmd args.
	if _, err := configupdate.ParseInterval(req.ServiceStatusInterval); err != nil {
		writeValidationError(w, err)
		return
	}
	intervalStr := rawStringPtr(req.ServiceStatusInterval)

	if err := h.store.SetServiceConfig(r.Context(), id, allowList, intervalStr); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	correlationID := cplog.CorrelationIDFromContext(r.Context())
	if correlationID == "" {
		correlationID = h.newCmdID()
	}
	cmd := envelope.Command{
		Type:          "config.update",
		CorrelationID: correlationID,
		CommandID:     h.newCmdID(),
		Args:          buildCmdArgs(allowList, intervalStr),
		IssuedAt:      time.Now().UTC(),
	}
	cmdBytes, _ := json.Marshal(cmd)

	if err := h.publisher.Publish(r.Context(), "devices/"+id+"/cmd", cmdBytes); err != nil {
		// Override is persisted on the cp side; the push failed. Surface
		// as 502 so the operator's UI shows the actionable cause; retry
		// is safe (SetServiceConfig is idempotent on re-write, the
		// publish topic is the only re-do needed).
		http.Error(w, "downstream publish failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(struct {
		CorrelationID string `json:"correlation_id"`
	}{CorrelationID: correlationID})
}

func writeValidationError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	if v, ok := configupdate.AsValidation(err); ok {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorBody{Code: v.Code, Message: v.Message})
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(errorBody{Code: configupdate.CodeBadPayload, Message: err.Error()})
}

// rawStringPtr extracts a *string from a raw JSON value that is either
// absent / null (returns nil) or a string. Validation has already
// confirmed shape; we just need the literal back out.
func rawStringPtr(raw json.RawMessage) *string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return &s
}

func buildCmdArgs(allowList *[]string, intervalStr *string) json.RawMessage {
	// Reconstruct the same wire shape the agent's parser accepts.
	// Skip absent fields entirely so the agent sees "leave alone" not
	// "set null"; explicit null and absent collapse to the same
	// semantics on the agent side, but absent keeps the payload small.
	m := map[string]any{}
	if allowList != nil {
		m["service_allow_list"] = *allowList
	}
	if intervalStr != nil {
		m["service_status_interval"] = *intervalStr
	}
	out, _ := json.Marshal(m)
	return out
}
