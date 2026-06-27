package settings

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// hostPressureBody is both the GET response and the PUT request shape for the
// host_net_pressure probe's CP-tunable scoring thresholds.
type hostPressureBody struct {
	EphemeralWarnPct float64 `json:"ephemeral_warn_pct"`
	EphemeralCritPct float64 `json:"ephemeral_crit_pct"`
	CloseWaitWarn    int     `json:"close_wait_warn"`
	CloseWaitCrit    int     `json:"close_wait_crit"`
}

// HostPressureGetHandler serves GET /settings/host-pressure — the effective
// thresholds (stored override or calibrated default per field). Staff-only.
type HostPressureGetHandler struct{ store Store }

func NewHostPressureGet(store Store) *HostPressureGetHandler {
	return &HostPressureGetHandler{store: store}
}

func (h *HostPressureGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t := h.effective(r)
	writeJSON(w, hostPressureBody(t))
}

// effective resolves each threshold from cp_settings, falling back per-field to
// the calibrated default — the same rules the ingester scores with.
func (h *HostPressureGetHandler) effective(r *http.Request) healthprobes.HostPressureThresholds {
	d := healthprobes.DefaultHostPressureThresholds
	getF := func(k string, fb float64) float64 {
		if v, _, err := h.store.GetCPSetting(r.Context(), k); err == nil && v != "" {
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				return n
			}
		}
		return fb
	}
	getI := func(k string, fb int) int {
		if v, _, err := h.store.GetCPSetting(r.Context(), k); err == nil && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		return fb
	}
	return healthprobes.HostPressureThresholds{
		EphemeralWarnPct: getF(registry.SettingHostPressureEphemeralWarnPct, d.EphemeralWarnPct),
		EphemeralCritPct: getF(registry.SettingHostPressureEphemeralCritPct, d.EphemeralCritPct),
		CloseWaitWarn:    getI(registry.SettingHostPressureCloseWaitWarn, d.CloseWaitWarn),
		CloseWaitCrit:    getI(registry.SettingHostPressureCloseWaitCrit, d.CloseWaitCrit),
	}
}

// HostPressurePutHandler serves PUT /settings/host-pressure — staff set all
// four thresholds in one call. Validated: percentages in (0,100], counts > 0,
// and warn strictly below crit for each pair. Staff-only.
type HostPressurePutHandler struct {
	store Store
	audit audit.Writer
}

func NewHostPressurePut(store Store, auditW audit.Writer) *HostPressurePutHandler {
	return &HostPressurePutHandler{store: store, audit: auditW}
}

func (h *HostPressurePutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	claims, _ := middleware.OperatorFromContext(r.Context())

	var req hostPressureBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.EphemeralWarnPct <= 0 || req.EphemeralCritPct > 100 || req.EphemeralWarnPct >= req.EphemeralCritPct {
		http.Error(w, "ephemeral percentages must satisfy 0 < warn < crit <= 100", http.StatusBadRequest)
		return
	}
	if req.CloseWaitWarn <= 0 || req.CloseWaitWarn >= req.CloseWaitCrit {
		http.Error(w, "close_wait counts must satisfy 0 < warn < crit", http.StatusBadRequest)
		return
	}

	sets := []struct {
		key string
		val string
	}{
		{registry.SettingHostPressureEphemeralWarnPct, strconv.FormatFloat(req.EphemeralWarnPct, 'f', -1, 64)},
		{registry.SettingHostPressureEphemeralCritPct, strconv.FormatFloat(req.EphemeralCritPct, 'f', -1, 64)},
		{registry.SettingHostPressureCloseWaitWarn, strconv.Itoa(req.CloseWaitWarn)},
		{registry.SettingHostPressureCloseWaitCrit, strconv.Itoa(req.CloseWaitCrit)},
	}
	for _, s := range sets {
		if err := h.store.SetCPSetting(r.Context(), s.key, s.val); err != nil {
			log.Error("set host-pressure threshold", "key", s.key, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.host_pressure_thresholds",
		ActorID:   claims.OperatorID,
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		Payload: map[string]any{
			"ephemeral_warn_pct": req.EphemeralWarnPct, "ephemeral_crit_pct": req.EphemeralCritPct,
			"close_wait_warn": req.CloseWaitWarn, "close_wait_crit": req.CloseWaitCrit,
		},
	})
	writeJSON(w, req)
}
