package taxonomy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

// RunTaskInvoker triggers the on-demand cp-taxonomy-sync Fargate task
// and returns its ARN. The handler keeps no state — concurrency is
// enforced by the task's own pg_try_advisory_lock (ADR-033 § 8).
type RunTaskInvoker interface {
	Run(ctx context.Context) (taskARN string, err error)
}

// SyncHandler serves POST /taxonomy/sync — the staff-only "Force
// sync now" button on the Settings page. Returns 202 + the new ECS
// task ARN; the caller refreshes the page manually to see updated
// counts via /taxonomy/status (no polling, no auto-refresh).
type SyncHandler struct {
	invoker RunTaskInvoker
	audit   audit.Writer
}

// NewSync binds a RunTaskInvoker and an audit sink.
func NewSync(inv RunTaskInvoker, auditW audit.Writer) *SyncHandler {
	return &SyncHandler{invoker: inv, audit: auditW}
}

type syncResponse struct {
	TaskARN string `json:"task_arn"`
}

func (h *SyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	claims, _ := middleware.OperatorFromContext(r.Context()) // staff-gate guaranteed

	arn, err := h.invoker.Run(r.Context())
	if err != nil {
		log.Error("audit.taxonomy_sync", "outcome", "error", "err", err)
		_ = h.audit.Write(r.Context(), audit.Entry{
			Action:    "audit.taxonomy_sync",
			ActorID:   claims.OperatorID,
			ActorType: audit.ActorOperator,
			Outcome:   "error",
			SourceIP:  clientIP(r),
			UserAgent: r.UserAgent(),
			Payload:   map[string]any{"err": err.Error()},
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.taxonomy_sync",
		ActorID:   claims.OperatorID,
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		Payload:   map[string]any{"task_arn": arn},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(syncResponse{TaskARN: arn})
}

// clientIP strips the port from r.RemoteAddr. Audit entries record
// the source for the operator who pulled the trigger.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
