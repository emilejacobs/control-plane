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
	"github.com/emilejacobs/control-plane/internal/protocol/logtail"
)

// LogTailStore is the persistence side of the log-tail flow. Narrow
// interface so the handlers test against a stub; *registry.Registry
// satisfies it in production.
type LogTailStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	CreateLogTailRequest(ctx context.Context, req registry.LogTailRequest) error
	GetLogTail(ctx context.Context, correlationID string) (registry.LogTail, error)
}

// === POST /devices/{id}/logs/tail ===

// LogTailPostHandler accepts an operator-initiated log tail request:
// validates the body, persists a pending row, publishes the log.tail
// cmd on the device's MQTT cmd topic, and returns 202 + correlation_id
// for the dashboard to poll. The cmd-result ACK lands separately via
// cp-ingest's CmdResultIngester.
type LogTailPostHandler struct {
	store     LogTailStore
	publisher CmdPublisher
	newCmdID  func() string
	now       func() time.Time
}

func NewLogTailPost(store LogTailStore, pub CmdPublisher) *LogTailPostHandler {
	return &LogTailPostHandler{
		store:     store,
		publisher: pub,
		newCmdID:  newRandomID,
		now:       time.Now,
	}
}

func (h *LogTailPostHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	req, err := logtail.Parse(raw)
	if err != nil {
		writeLogTailValidationError(w, err)
		return
	}

	correlationID := cplog.CorrelationIDFromContext(r.Context())
	if correlationID == "" {
		correlationID = h.newCmdID()
	}

	if err := h.store.CreateLogTailRequest(r.Context(), registry.LogTailRequest{
		CorrelationID:  correlationID,
		DeviceID:       id,
		LogName:        req.LogName,
		LinesRequested: req.Lines,
		RequestedAt:    h.now().UTC(),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cmd := envelope.Command{
		Type:          "log.tail",
		CorrelationID: correlationID,
		CommandID:     h.newCmdID(),
		Args:          raw,
		IssuedAt:      h.now().UTC(),
	}
	cmdBytes, _ := json.Marshal(cmd)

	if err := h.publisher.Publish(r.Context(), "devices/"+id+"/cmd", cmdBytes); err != nil {
		// Pending row persists; operator can retry. The agent never
		// got the cmd, so the row will stay pending forever unless
		// the sweeper cleans it OR a retry succeeds.
		http.Error(w, "downstream publish failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(struct {
		CorrelationID string `json:"correlation_id"`
	}{CorrelationID: correlationID})
}

func writeLogTailValidationError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	if v, ok := logtail.AsValidation(err); ok {
		_ = json.NewEncoder(w).Encode(errorBody{Code: v.Code, Message: v.Message})
		return
	}
	_ = json.NewEncoder(w).Encode(errorBody{Code: logtail.CodeBadPayload, Message: err.Error()})
}

// === GET /devices/{id}/logs/tail/{correlation_id} ===

// LogTailGetHandler is the dashboard's poll target. Returns the row's
// current state (status, content, truncation, error). 404 if either
// the device is out of scope or the correlation_id is unknown.
type LogTailGetHandler struct {
	store LogTailStore
}

func NewLogTailGet(store LogTailStore) *LogTailGetHandler {
	return &LogTailGetHandler{store: store}
}

type logTailResponse struct {
	CorrelationID  string  `json:"correlation_id"`
	LogName        string  `json:"log_name"`
	LinesRequested int     `json:"lines_requested"`
	Status         string  `json:"status"`
	Content        *string `json:"content"`
	Truncated      bool    `json:"truncated"`
	TruncatedFrom  *int    `json:"truncated_from"`
	ErrorCode      *string `json:"error_code"`
	ErrorMessage   *string `json:"error_message"`
	RequestedAt    string  `json:"requested_at"`
	ReturnedAt     *string `json:"returned_at"`
}

func (h *LogTailGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	corrID := r.PathValue("correlation_id")

	// Device-scope check first — operator can only see tails for
	// devices they have access to.
	if _, err := h.store.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	t, err := h.store.GetLogTail(r.Context(), corrID)
	if err != nil {
		if errors.Is(err, registry.ErrLogTailNotFound) {
			http.Error(w, "log tail not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Defence in depth: the correlation_id is also tied to a device_id
	// in the row. If they don't match, treat as not-found rather than
	// surfacing one operator's tail to another.
	if t.DeviceID != id {
		http.Error(w, "log tail not found", http.StatusNotFound)
		return
	}

	var returnedAt *string
	if t.ReturnedAt != nil {
		s := t.ReturnedAt.UTC().Format(time.RFC3339)
		returnedAt = &s
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(logTailResponse{
		CorrelationID:  t.CorrelationID,
		LogName:        t.LogName,
		LinesRequested: t.LinesRequested,
		Status:         t.Status,
		Content:        t.Content,
		Truncated:      t.Truncated,
		TruncatedFrom:  t.TruncatedFrom,
		ErrorCode:      t.ErrorCode,
		ErrorMessage:   t.ErrorMessage,
		RequestedAt:    t.RequestedAt.UTC().Format(time.RFC3339),
		ReturnedAt:     returnedAt,
	})
}
