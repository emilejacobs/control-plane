package settings

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"net/url"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// notificationsResponse is the GET /settings/notifications shape. The Teams
// webhook is write-only: only whether it is set and a host-only preview are
// exposed, never the signed URL.
type notificationsResponse struct {
	Enabled             bool     `json:"enabled"`
	EmailRecipients     []string `json:"email_recipients"`
	TeamsWebhookSet     bool     `json:"teams_webhook_set"`
	TeamsWebhookPreview string   `json:"teams_webhook_preview"`
}

// NotificationsGetHandler serves GET /settings/notifications — the enable
// switch, recipient list, and Teams-webhook set/preview. Staff-only.
type NotificationsGetHandler struct{ store Store }

func NewNotificationsGet(store Store) *NotificationsGetHandler {
	return &NotificationsGetHandler{store: store}
}

func (h *NotificationsGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())

	enabledVal, _, err := h.store.GetCPSetting(r.Context(), registry.SettingNotificationsEnabled)
	if err != nil {
		log.Error("get notifications enabled", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	recipientsVal, _, err := h.store.GetCPSetting(r.Context(), registry.SettingNotificationsRecipients)
	if err != nil {
		log.Error("get notifications recipients", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	webhookVal, webhookSet, err := h.store.GetCPSetting(r.Context(), registry.SettingTeamsWebhookURL)
	if err != nil {
		log.Error("get teams webhook", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, notificationsResponse{
		Enabled:             enabledVal == "true",
		EmailRecipients:     decodeRecipients(recipientsVal),
		TeamsWebhookSet:     webhookSet && webhookVal != "",
		TeamsWebhookPreview: webhookPreview(webhookVal),
	})
}

// NotificationsPutHandler serves PUT /settings/notifications — staff set the
// enable switch and recipient list (non-secret config) in one call.
type NotificationsPutHandler struct {
	store Store
	audit audit.Writer
}

func NewNotificationsPut(store Store, auditW audit.Writer) *NotificationsPutHandler {
	return &NotificationsPutHandler{store: store, audit: auditW}
}

type notificationsRequest struct {
	Enabled         bool     `json:"enabled"`
	EmailRecipients []string `json:"email_recipients"`
}

func (h *NotificationsPutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	claims, _ := middleware.OperatorFromContext(r.Context()) // staff-gate guaranteed

	var req notificationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	for _, addr := range req.EmailRecipients {
		if _, err := mail.ParseAddress(addr); err != nil {
			http.Error(w, "invalid email address: "+addr, http.StatusBadRequest)
			return
		}
	}

	enabled := "false"
	if req.Enabled {
		enabled = "true"
	}
	recipients, _ := json.Marshal(req.EmailRecipients)

	if err := h.store.SetCPSetting(r.Context(), registry.SettingNotificationsEnabled, enabled); err != nil {
		log.Error("set notifications enabled", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.store.SetCPSetting(r.Context(), registry.SettingNotificationsRecipients, string(recipients)); err != nil {
		log.Error("set notifications recipients", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.notifications_config",
		ActorID:   claims.OperatorID,
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		Payload:   map[string]any{"enabled": req.Enabled, "recipient_count": len(req.EmailRecipients)},
	})
	writeJSON(w, notificationsResponse{
		Enabled:         req.Enabled,
		EmailRecipients: nonNilStrings(req.EmailRecipients),
	})
}

// TeamsWebhookPutHandler serves PUT /settings/notifications/teams-webhook —
// staff set the write-only Teams webhook URL. The URL is a signed bearer
// credential: kept out of the audit payload and logs.
type TeamsWebhookPutHandler struct {
	store Store
	audit audit.Writer
}

func NewTeamsWebhookPut(store Store, auditW audit.Writer) *TeamsWebhookPutHandler {
	return &TeamsWebhookPutHandler{store: store, audit: auditW}
}

type teamsWebhookRequest struct {
	WebhookURL string `json:"webhook_url"`
}

func (h *TeamsWebhookPutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	claims, _ := middleware.OperatorFromContext(r.Context()) // staff-gate guaranteed

	var req teamsWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.WebhookURL == "" {
		http.Error(w, "webhook_url required", http.StatusBadRequest)
		return
	}
	if u, err := url.ParseRequestURI(req.WebhookURL); err != nil || u.Scheme != "https" {
		http.Error(w, "webhook_url must be an https URL", http.StatusBadRequest)
		return
	}

	if err := h.store.SetCPSetting(r.Context(), registry.SettingTeamsWebhookURL, req.WebhookURL); err != nil {
		log.Error("audit.teams_webhook", "outcome", "error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.teams_webhook",
		ActorID:   claims.OperatorID,
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		// No webhook URL in the payload — it is a signed secret.
		Payload: map[string]any{"is_set": true},
	})
	writeJSON(w, isSetResponse{IsSet: true})
}

// decodeRecipients parses the stored JSON array, tolerating an unset/empty
// value by returning an empty slice (so the API field is always []).
func decodeRecipients(stored string) []string {
	if stored == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(stored), &out); err != nil {
		return []string{}
	}
	return nonNilStrings(out)
}

// webhookPreview returns a host-only, non-sensitive hint of the configured
// webhook (no path, no query, no signature). Empty for an unset webhook.
func webhookPreview(stored string) string {
	if stored == "" {
		return ""
	}
	if u, err := url.Parse(stored); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
