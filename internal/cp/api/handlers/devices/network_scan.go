package devices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
)

// NetworkScanStore is the persistence side of the network-scan
// handlers. Narrow interface so the handlers test against a stub;
// *registry.Registry satisfies it in production.
type NetworkScanStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	CreateNetworkScanRequest(ctx context.Context, req registry.NetworkScanRequest) error
	GetNetworkScan(ctx context.Context, correlationID string) (registry.NetworkScan, error)
}

// === POST /devices/{id}/network-scan ===

// NetworkScanPostHandler accepts an operator-initiated LAN scan
// request: validates the body, persists a pending row, publishes the
// network.scan cmd on the device's MQTT cmd topic, and returns 202 +
// correlation_id for the dashboard to poll. The cmd-result ACK lands
// separately via cp-ingest's CmdResultIngester.
type NetworkScanPostHandler struct {
	store     NetworkScanStore
	publisher CmdPublisher
	newCmdID  func() string
	now       func() time.Time
}

func NewNetworkScanPost(store NetworkScanStore, pub CmdPublisher) *NetworkScanPostHandler {
	return &NetworkScanPostHandler{
		store:     store,
		publisher: pub,
		newCmdID:  newRandomID,
		now:       time.Now,
	}
}

func (h *NetworkScanPostHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	req, err := networkscan.ParseRequest(raw)
	if err != nil {
		writeNetworkScanValidationError(w, err)
		return
	}

	correlationID := cplog.CorrelationIDFromContext(r.Context())
	if correlationID == "" {
		correlationID = h.newCmdID()
	}

	if err := h.store.CreateNetworkScanRequest(r.Context(), registry.NetworkScanRequest{
		CorrelationID: correlationID,
		DeviceID:      id,
		CIDR:          req.CIDR,
		RequestedAt:   h.now().UTC(),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-encode the parsed Request rather than forwarding the raw body —
	// guards against a payload that passed the whitelist but the agent
	// would re-validate differently (e.g. comments, whitespace
	// differences). The agent's ParseRequest accepts what CP's accepted.
	args, _ := json.Marshal(req)
	cmd := envelope.Command{
		Type:          "network.scan",
		CorrelationID: correlationID,
		CommandID:     h.newCmdID(),
		Args:          args,
		IssuedAt:      h.now().UTC(),
	}
	cmdBytes, _ := json.Marshal(cmd)

	if err := h.publisher.Publish(r.Context(), "devices/"+id+"/cmd", cmdBytes); err != nil {
		// Pending row persists; operator can retry. The agent never
		// got the cmd, so the row stays pending until the sweeper or
		// a successful retry cleans it.
		http.Error(w, "downstream publish failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(struct {
		CorrelationID string `json:"correlation_id"`
	}{CorrelationID: correlationID})
}

func writeNetworkScanValidationError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	if v, ok := networkscan.AsValidation(err); ok {
		_ = json.NewEncoder(w).Encode(errorBody{Code: v.Code, Message: v.Message})
		return
	}
	_ = json.NewEncoder(w).Encode(errorBody{Code: networkscan.CodeBadPayload, Message: err.Error()})
}

// === GET /devices/{id}/network-scan/{correlation_id} ===

// NetworkScanGetHandler is the dashboard's poll target. Returns the
// row's current state (status, result, error). 404 if either the
// device is out of scope or the correlation_id is unknown or belongs
// to a different device.
type NetworkScanGetHandler struct {
	store NetworkScanStore
}

func NewNetworkScanGet(store NetworkScanStore) *NetworkScanGetHandler {
	return &NetworkScanGetHandler{store: store}
}

type networkScanResponse struct {
	CorrelationID string                `json:"correlation_id"`
	CIDR          *string               `json:"cidr"`
	Status        string                `json:"status"`
	Result        *networkscan.Response `json:"result"`
	ErrorCode     *string               `json:"error_code"`
	ErrorMessage  *string               `json:"error_message"`
	RequestedAt   string                `json:"requested_at"`
	ReturnedAt    *string               `json:"returned_at"`
}

func (h *NetworkScanGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	corrID := r.PathValue("correlation_id")

	// Device-scope check first — the operator can only see scans for
	// devices they have access to.
	if _, err := h.store.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	n, err := h.store.GetNetworkScan(r.Context(), corrID)
	if err != nil {
		if errors.Is(err, registry.ErrNetworkScanNotFound) {
			http.Error(w, "network scan not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Defence in depth: the correlation_id is also tied to a device_id
	// in the row. If they don't match, treat as not-found rather than
	// surface one operator's scan to another.
	if n.DeviceID != id {
		http.Error(w, "network scan not found", http.StatusNotFound)
		return
	}

	var returnedAt *string
	if n.ReturnedAt != nil {
		s := n.ReturnedAt.UTC().Format(time.RFC3339)
		returnedAt = &s
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(networkScanResponse{
		CorrelationID: n.CorrelationID,
		CIDR:          n.CIDR,
		Status:        n.Status,
		Result:        n.Result,
		ErrorCode:     n.ErrorCode,
		ErrorMessage:  n.ErrorMessage,
		RequestedAt:   n.RequestedAt.UTC().Format(time.RFC3339),
		ReturnedAt:    returnedAt,
	})
}
