package devices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/captures"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	snapshotproto "github.com/emilejacobs/control-plane/internal/protocol/camerasnapshot"
	"github.com/emilejacobs/control-plane/internal/protocol/upload"
)

// snapshotPutTTL bounds how long the presigned PUT URL embedded in the
// camera.snapshot command stays valid — enough for ffmpeg + the upload.
const snapshotPutTTL = 5 * time.Minute

// SnapshotStore is the scope-checking slice the snapshot trigger needs.
// *registry.Registry satisfies it; GetByID is site-scoped so an out-of-scope
// device 404s.
type SnapshotStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
}

// === POST /devices/{id}/snapshot ===

// CameraSnapshotHandler triggers an on-demand snapshot (issue #8, ADR-030 § 7).
// CP mints the S3 key + presigns the PUT here and embeds both in the
// camera.snapshot cmd, so the agent uploads directly (no upload.request round
// trip — that would deadlock the agent's ordered command router). The resulting
// device_captures row is indexed by cp-ingest from the cmd-result ACK; the
// dashboard surfaces it by re-fetching the captures list.
type CameraSnapshotHandler struct {
	store     SnapshotStore
	presigner captures.Presigner
	publisher CmdPublisher
	newID     func() string
	now       func() time.Time
}

func NewCameraSnapshot(store SnapshotStore, presigner captures.Presigner, pub CmdPublisher) *CameraSnapshotHandler {
	return &CameraSnapshotHandler{
		store:     store,
		presigner: presigner,
		publisher: pub,
		newID:     newRandomID,
		now:       time.Now,
	}
}

func (h *CameraSnapshotHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		CameraID string `json:"camera_id"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeSnapshotError(w, snapshotproto.CodeBadPayload, "invalid JSON body")
		return
	}
	if body.CameraID == "" {
		writeSnapshotError(w, snapshotproto.CodeBadPayload, "camera_id is required")
		return
	}

	correlationID := cplog.CorrelationIDFromContext(r.Context())
	if correlationID == "" {
		correlationID = h.newID()
	}

	s3Key, err := upload.S3Key(upload.KindSnapshot, id, h.newID(), snapshotproto.ContentType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	putURL, err := h.presigner.PutURL(r.Context(), s3Key, snapshotproto.ContentType, snapshotPutTTL)
	if err != nil {
		http.Error(w, "presign upload URL: "+err.Error(), http.StatusInternalServerError)
		return
	}

	args, _ := json.Marshal(snapshotproto.Args{CameraID: body.CameraID, S3Key: s3Key, PutURL: putURL})
	cmd := envelope.Command{
		Type:          "camera.snapshot",
		CorrelationID: correlationID,
		CommandID:     h.newID(),
		Args:          args,
		IssuedAt:      h.now().UTC(),
	}
	cmdBytes, _ := json.Marshal(cmd)

	if err := h.publisher.Publish(r.Context(), "devices/"+id+"/cmd", cmdBytes); err != nil {
		http.Error(w, "downstream publish failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(struct {
		CorrelationID string `json:"correlation_id"`
		S3Key         string `json:"s3_key"`
	}{CorrelationID: correlationID, S3Key: s3Key})
}

func writeSnapshotError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}
