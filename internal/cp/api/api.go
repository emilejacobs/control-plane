// Package api wires the Control Plane HTTP router.
//
// State-mutating routes (POST/PUT/PATCH/DELETE) are registered through
// Builder.Post / .Put / .Patch / .Delete, which automatically wrap the
// handler in the idempotency middleware. Read-side routes go through
// Builder.Get. Tests use NewBuilderWith + Builder.MutatingRoutes to
// enforce that every mutator is wrapped (the ADR-012 CI gate).
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/agentrollout"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/auth"
	captureshttp "github.com/emilejacobs/control-plane/internal/cp/api/handlers/captures"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/enrollment"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/fleet"
	operatorshttp "github.com/emilejacobs/control-plane/internal/cp/api/handlers/operators"
	settingshttp "github.com/emilejacobs/control-plane/internal/cp/api/handlers/settings"
	taxonomyhttp "github.com/emilejacobs/control-plane/internal/cp/api/handlers/taxonomy"
	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/authz"
	"github.com/emilejacobs/control-plane/internal/cp/captures"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/operators"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/taxonomy"
)

type Deps struct {
	Registry         *registry.Registry
	AuthN            *authn.AuthN
	AuthZ            *authz.AuthZ
	IdempotencyStore middleware.IdempotencyStore

	// Operators is the #16 operator-management store backing the staff-only
	// /operators endpoints. nil disables those routes (the rest of the API
	// continues to serve; tests that don't exercise the surface omit it).
	Operators *operators.Store

	// CapturePresigner mints signed S3 URLs for the captures read surface
	// (#8). nil disables GET /captures/{id}/url (the list route still serves).
	CapturePresigner captures.Presigner

	// TaxonomyStore is the clients/sites mirror store ADR-033 § 8's
	// GET /taxonomy/status reads. nil disables the route — tests that
	// don't exercise the surface omit it.
	TaxonomyStore *taxonomy.Store

	// TaxonomyRunTask triggers an on-demand ecs:RunTask for the
	// cp-taxonomy-sync task def (ADR-033 § 3). Bound by the cp-api
	// main to a real ECS client; tests inject a fake. nil disables
	// POST /taxonomy/sync (the read surface still works).
	TaxonomyRunTask RunTaskInvoker

	// CmdPublisher is the IoT-Core data-plane publisher used by the
	// Phase 2 slice 2 service-config PUT to push a config.update down
	// the cmd channel. nil disables the route (the rest of the API
	// continues to serve; tests that don't need the surface omit it).
	CmdPublisher devices.CmdPublisher

	// Commissioner runs the staff-only Commission action (#91): mint a
	// per-device Tailscale key, gather the ALPR license + PR token, and push
	// cameras + secrets to the device. nil disables POST
	// /devices/{id}/commission — deploys before the Tailscale credential is
	// configured keep serving.
	Commissioner devices.Commissioner

	// AgentRolloutCatalog reads signed release manifests from agent-dist
	// and AgentRolloutPusher fans agent.update out to a device set
	// (issue #40). Both nil disables POST /agent-rollouts — deploys that
	// land before AGENT_DIST_BUCKET is configured keep serving.
	AgentRolloutCatalog agentrollout.ManifestSource
	AgentRolloutPusher  devices.UpdatePusher

	// AgentVersionCatalog lists the published catalog versions that back the
	// rollout target picker (GET /fleet/agent-versions, #42). Same agent-dist
	// source as AgentRolloutCatalog; nil disables the route.
	AgentVersionCatalog fleet.VersionCatalog

	// Audit is the sink every state-mutating handler writes audit entries
	// through. nil falls back to a discard Writer so tests that do not
	// care about audit assertions can omit it.
	Audit audit.Writer

	// Logger is the base slog.Logger that cplog.Middleware wraps per
	// request. nil falls back to slog.Default(); tests pass a discard
	// logger to keep -v output clean.
	Logger *slog.Logger

	// CORSAllowedOrigins is the exact-match allow list for the CORS
	// middleware. Empty disables CORS. Production passes the dashboard
	// origin (https://control.uknomi.com) from main; tests pass nothing
	// unless they specifically need to exercise CORS.
	CORSAllowedOrigins []string
}

// RunTaskInvoker triggers a one-shot Fargate task and returns its
// ARN. Production wires an ECS-backed adapter; tests inject a
// recording fake. The handler stays free of the AWS SDK surface.
type RunTaskInvoker interface {
	Run(ctx context.Context) (taskARN string, err error)
}

// Route names a method + path pair for the CI-gate test to probe.
type Route struct {
	Method string
	Path   string
}

// Builder constructs the CP router. State-mutating registrations
// auto-wrap the handler in the idempotency middleware + the audit
// HTTP middleware (so a handler that forgets to call audit.Write still
// records an envelope row) and record the route so tests can enumerate them.
type Builder struct {
	mux      *http.ServeMux
	idem     func(http.Handler) http.Handler
	auditMW  func(http.Handler) http.Handler
	mutating []Route
}

func newBuilder(idem, auditMW func(http.Handler) http.Handler) *Builder {
	return &Builder{mux: http.NewServeMux(), idem: idem, auditMW: auditMW}
}

// Get registers a read-side route. No idempotency or audit wrapping.
func (b *Builder) Get(path string, h http.Handler) {
	b.mux.Handle("GET "+path, h)
}

// Post registers a state-mutating route. The handler is wrapped in
// audit (innermost — sees the handler's status) then idempotency
// (outermost — short-circuits before the handler runs). The route is
// recorded for the CI-gate test.
func (b *Builder) Post(path string, h http.Handler) {
	b.mux.Handle("POST "+path, b.idem(b.auditMW(h)))
	b.mutating = append(b.mutating, Route{Method: http.MethodPost, Path: path})
}

// Put registers a state-mutating PUT route. Same middleware stack as
// Post (audit innermost, idempotency outermost); recorded for the CI-
// gate test on the same footing.
func (b *Builder) Put(path string, h http.Handler) {
	b.mux.Handle("PUT "+path, b.idem(b.auditMW(h)))
	b.mutating = append(b.mutating, Route{Method: http.MethodPut, Path: path})
}

// Delete registers a state-mutating DELETE route. Same middleware
// stack as Post / Put (audit innermost, idempotency outermost);
// recorded for the CI-gate test on the same footing.
func (b *Builder) Delete(path string, h http.Handler) {
	b.mux.Handle("DELETE "+path, b.idem(b.auditMW(h)))
	b.mutating = append(b.mutating, Route{Method: http.MethodDelete, Path: path})
}

// PostNoIdem registers a POST route that is audited but NOT behind the
// idempotency middleware, and is NOT recorded as a mutating route for the
// idempotency CI gate. Used for the /auth/* token-rotation endpoints: an
// idempotency key on a single-use rotation (login/refresh/logout) is
// semantically wrong — a replayed refresh must fail, not replay cached
// tokens — and requiring the header would needlessly break non-browser
// clients (mobile, curl). State-mutating resource endpoints keep Post.
func (b *Builder) PostNoIdem(path string, h http.Handler) {
	b.mux.Handle("POST "+path, b.auditMW(h))
}

// Handler returns the underlying mux for serving.
func (b *Builder) Handler() http.Handler { return b.mux }

// MutatingRoutes returns the recorded state-mutating routes. Tests use this
// to verify each one rejects requests without Idempotency-Key.
func (b *Builder) MutatingRoutes() []Route { return b.mutating }

// enrollmentRateLimit caps a single source IP at 20 enrollment requests per
// hour (ADR-017) — bursty real waves stay well under it; a leaked bootstrap
// key cannot enroll an unbounded number of fake devices.
const (
	enrollmentRateLimit  = 20
	enrollmentRateWindow = time.Hour
)

// NewBuilderWith returns a fully-configured Builder. Tests use this to
// inspect the route table; production code uses NewRouter.
func NewBuilderWith(d Deps) *Builder {
	auditW := d.Audit
	if auditW == nil {
		auditW = audit.SlogOnly{}
	}
	b := newBuilder(middleware.Idempotency(d.IdempotencyStore), audit.HTTPMiddleware(auditW))
	// /healthz is the ALB target group health check (ADR-022). 200, empty body,
	// no auth. Tightening from 200-499 to 200 depends on this being live.
	b.Get("/healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	enrollLimiter := middleware.NewRateLimiter(enrollmentRateLimit, enrollmentRateWindow)
	b.Post("/enrollments", enrollLimiter.Middleware(enrollment.New(d.Registry, auditW)))
	if d.AuthN != nil {
		b.Get("/auth/first-run", auth.NewFirstRunStatus(d.AuthN))
		// /auth/* are token-rotation endpoints, not idempotent resource
		// mutations — they sit outside the idempotency gate (see PostNoIdem),
		// so any client can authenticate without minting an Idempotency-Key.
		b.PostNoIdem("/auth/first-run", auth.NewFirstRun(d.AuthN, auditW))
		b.PostNoIdem("/auth/login", auth.NewLogin(d.AuthN, auditW))
		b.PostNoIdem("/auth/refresh", auth.NewRefresh(d.AuthN, auditW))
		b.PostNoIdem("/auth/logout", auth.NewLogout(d.AuthN, auditW))
		// Authenticated routes require a valid operator bearer token.
		// Every authenticated route except enrollment itself also sits
		// behind the forced-TOTP-enrollment gate; device reads additionally
		// run through the site-scope middleware.
		requireAuth := middleware.Auth(d.AuthN)
		requireEnrolled := middleware.RequireTotpEnrolled(d.AuthN)
		requireScope := middleware.Scope(d.AuthZ)
		// #16: an operator on a system-generated temp password must set a new
		// one before any normal action. The gate wraps every protected route
		// — including TOTP enrollment, so the order is set-password → enroll
		// → access — except POST /auth/password itself (below), which sits
		// behind Auth only so a must-change operator can reach it.
		requirePwChanged := middleware.RequirePasswordChanged(d.AuthN)
		// onboarded composes the two first-login gates every fully-onboarded
		// route shares: TOTP enrolled AND temp password rotated. Used in
		// place of the bare TOTP gate so an admin password-reset (which
		// re-arms must-change on an already-enrolled operator) also blocks
		// normal access until the operator rotates.
		onboarded := func(h http.Handler) http.Handler { return requireEnrolled(requirePwChanged(h)) }
		requireStaff := middleware.RequireStaff()
		// staffScoped gates a per-device action to staff AND injects the
		// operator's SiteFilter (All for staff) — the same scope requireScope
		// supplies the read routes. Device handlers verify existence through the
		// site-scoped GetByID, which fails closed (→ 404 "device not found")
		// when no scope is in context; a staff route that only ran requireStaff
		// had none, so every staff device mutation 404'd. requireStaff stays
		// outermost so a non-staff caller is rejected before any scope work.
		staffScoped := func(h http.Handler) http.Handler { return requireStaff(requireScope(h)) }
		b.PostNoIdem("/auth/password", requireAuth(auth.NewSetPassword(d.AuthN, auditW)))
		b.PostNoIdem("/auth/totp/enroll", requireAuth(requirePwChanged(auth.NewTotpEnroll(d.AuthN, auditW))))
		b.Get("/devices", requireAuth(onboarded(requireScope(devices.NewList(d.Registry)))))
		b.Get("/devices/{id}", requireAuth(onboarded(requireScope(devices.NewGet(d.Registry)))))
		// PUT /devices/{id}/snapshot-config — set the per-device scheduled-
		// snapshot cadence (#9): persists it and (when CmdPublisher is wired)
		// pushes snapshot.config to the agent. Same auth + TOTP + site scope.
		b.Put("/devices/{id}/snapshot-config",
			requireAuth(onboarded(requireScope(devices.NewSnapshotConfig(d.Registry, d.CmdPublisher)))))
		// Phase 2 fleet health probes (issue #19): per-device probe
		// snapshot. Read-only; same auth + TOTP + site scope as the
		// other per-device reads.
		b.Get("/devices/{id}/health-probes", requireAuth(onboarded(requireScope(devices.NewHealthProbeList(d.Registry)))))
		// GET /fleet/alerts — fleet-wide roll-up for the Overview alerts
		// dashboard (#21). Site-scoped (not staff-gated): a scoped operator
		// sees only their sites' alerts; staff see the whole fleet.
		b.Get("/fleet/alerts", requireAuth(onboarded(requireScope(fleet.NewAlerts(d.Registry)))))
		// GET /fleet/agent-rollout — issue #40 desired-vs-reported rollout
		// view. Site-scoped like /fleet/alerts: scoped operators see their
		// slice, staff see the fleet; the mutating counterpart
		// (POST /agent-rollouts, below) stays staff-only.
		b.Get("/fleet/agent-rollout", requireAuth(onboarded(requireScope(fleet.NewAgentRollout(d.Registry)))))
		// GET /fleet/agent-versions — published catalog versions for the
		// rollout target picker (#42). Auth + TOTP but not staff-gated and
		// not site-scoped (mirrors GET /sites): the version catalog is
		// fleet-global, and operators see it to populate the picker; the
		// rollout itself (POST /agent-rollouts) stays staff-only. Skipped
		// when agent-dist isn't configured.
		if d.AgentVersionCatalog != nil {
			b.Get("/fleet/agent-versions",
				requireAuth(onboarded(fleet.NewAgentVersions(d.AgentVersionCatalog))))
		}
		// Captures read surface (#8): per-device list + signed download URL.
		// Site-scoped device reads. The URL route needs the presigner.
		b.Get("/devices/{id}/captures", requireAuth(onboarded(requireScope(captureshttp.NewList(d.Registry)))))
		if d.CapturePresigner != nil {
			b.Get("/captures/{id}/url", requireAuth(onboarded(requireScope(captureshttp.NewURL(d.Registry, d.CapturePresigner)))))
			// On-demand snapshot (#8 Slice B): presign a PUT + push camera.snapshot.
			// Needs both the presigner (to sign the upload) and CmdPublisher (to
			// push the cmd); gated on both being wired.
			if d.CmdPublisher != nil {
				b.Post("/devices/{id}/snapshot",
					requireAuth(onboarded(requireScope(devices.NewCameraSnapshot(d.Registry, d.CapturePresigner, d.CmdPublisher)))))
			}
		}
		// Phase 2 edge-UI rework: cameras inventory CRUD (issue #2).
		// Read route (GET) requires only auth + TOTP + site scope.
		// Mutating routes additionally need CmdPublisher to push the
		// cameras.update cmd down to the agent after each change.
		b.Get("/devices/{id}/cameras", requireAuth(onboarded(requireScope(devices.NewCameraList(d.Registry)))))
		if d.CmdPublisher != nil {
			b.Post("/devices/{id}/cameras",
				requireAuth(onboarded(requireScope(devices.NewCameraPost(d.Registry, d.CmdPublisher)))))
			b.Put("/devices/{id}/cameras/{camera_id}",
				requireAuth(onboarded(requireScope(devices.NewCameraPut(d.Registry, d.CmdPublisher)))))
			b.Delete("/devices/{id}/cameras/{camera_id}",
				requireAuth(onboarded(requireScope(devices.NewCameraDelete(d.Registry, d.CmdPublisher)))))
		}
		// Phase 2 slice 2: PUT /devices/{id}/service-config. Requires
		// auth + TOTP + site scope (same gates as the read surface).
		// Skipped silently when CmdPublisher is nil — keeps tests that
		// don't exercise the downward channel running unchanged.
		if d.CmdPublisher != nil {
			b.Put("/devices/{id}/service-config",
				requireAuth(onboarded(requireScope(devices.NewServiceConfigPut(d.Registry, d.CmdPublisher)))))
			// POST /devices/{id}/config-backfill (#85) — staff-only push of
			// install-time-only config fields (snapshot_state_path) to an
			// already-enrolled device; takes effect on the agent's next restart.
			b.Post("/devices/{id}/config-backfill",
				requireAuth(onboarded(staffScoped(devices.NewConfigBackfill(d.Registry, d.CmdPublisher, auditW)))))
			// Phase 2 slice 3: log-tail. POST initiates a tail request,
			// publishes the log.tail cmd; GET polls the per-request row
			// until status flips to done|error. Same CmdPublisher gate
			// as service-config (no publisher → no downward surfaces).
			b.Post("/devices/{id}/logs/tail",
				requireAuth(onboarded(requireScope(devices.NewLogTailPost(d.Registry, d.CmdPublisher)))))
			b.Get("/devices/{id}/logs/tail/{correlation_id}",
				requireAuth(onboarded(requireScope(devices.NewLogTailGet(d.Registry)))))
			// Phase 2 Edge UI rework (issue #3): network scan. POST
			// triggers a LAN scan via the network.scan cmd; GET polls
			// the per-request row for the agent's hosts list. Same
			// CmdPublisher gate as the surfaces above.
			b.Post("/devices/{id}/network-scan",
				requireAuth(onboarded(requireScope(devices.NewNetworkScanPost(d.Registry, d.CmdPublisher)))))
			b.Get("/devices/{id}/network-scan/{correlation_id}",
				requireAuth(onboarded(requireScope(devices.NewNetworkScanGet(d.Registry)))))
		}
		// ADR-033 § 8 / Issue #18 — clients/sites taxonomy sync.
		// Staff-only: the manual button is admin-only (non-zero ECS
		// cost) and the status surface mirrors that scope. Read-only
		// status here; the RunTask trigger lands in the next slice.
		// #16 — staff-only operator management. Create/edit/deactivate are
		// mutating; deactivate/reactivate are POST sub-routes (not DELETE) so
		// the soft-delete semantics read clearly and stay auditable.
		if d.Operators != nil {
			// Account-wide Plate Recognizer token (#84) — staff-only. GET
			// reports only whether it's set; PUT stores the secret (pushed to
			// devices at Commission, #91).
			b.Get("/settings/pr-token", requireAuth(onboarded(requireStaff(settingshttp.NewPRTokenGet(d.Registry)))))
			b.Put("/settings/pr-token", requireAuth(onboarded(requireStaff(settingshttp.NewPRTokenPut(d.Registry, auditW)))))
			// Fleet notification config (#96) — staff-only. Enable switch +
			// recipient list via /settings/notifications; the write-only Teams
			// webhook secret via its own endpoint.
			b.Get("/settings/notifications", requireAuth(onboarded(requireStaff(settingshttp.NewNotificationsGet(d.Registry)))))
			b.Put("/settings/notifications", requireAuth(onboarded(requireStaff(settingshttp.NewNotificationsPut(d.Registry, auditW)))))
			b.Put("/settings/notifications/teams-webhook", requireAuth(onboarded(requireStaff(settingshttp.NewTeamsWebhookPut(d.Registry, auditW)))))
			b.Get("/operators", requireAuth(onboarded(requireStaff(operatorshttp.NewList(d.Operators)))))
			b.Get("/operators/{id}", requireAuth(onboarded(requireStaff(operatorshttp.NewGet(d.Operators)))))
			b.Post("/operators", requireAuth(onboarded(requireStaff(operatorshttp.NewCreate(d.Operators, auditW)))))
			b.Put("/operators/{id}", requireAuth(onboarded(requireStaff(operatorshttp.NewUpdate(d.Operators, auditW)))))
			b.Post("/operators/{id}/deactivate",
				requireAuth(onboarded(requireStaff(operatorshttp.NewSetActive(d.Operators, auditW, false)))))
			b.Post("/operators/{id}/reactivate",
				requireAuth(onboarded(requireStaff(operatorshttp.NewSetActive(d.Operators, auditW, true)))))
		}
		// ADR-033 § 8 / Issue #18 — clients/sites taxonomy sync.
		if d.TaxonomyStore != nil {
			b.Get("/taxonomy/status",
				requireAuth(onboarded(requireStaff(taxonomyhttp.NewStatus(d.TaxonomyStore)))))
			if d.TaxonomyRunTask != nil {
				b.Post("/taxonomy/sync",
					requireAuth(onboarded(requireStaff(taxonomyhttp.NewSync(d.TaxonomyRunTask, auditW)))))
			}
			// GET /sites is the picker surface for the
			// device-deployment edit modal: auth + TOTP, but not
			// staff-gated — operators see the catalog so the device
			// list can render real client/site names. Edit endpoints
			// stay staff-only (PUT /devices/{id}/deployment below).
			b.Get("/sites",
				requireAuth(onboarded(taxonomyhttp.NewSites(d.TaxonomyStore))))
			// PUT /devices/{id}/deployment — staff-only edit of a
			// device's site assignment + asset_number. The picker
			// (GET /sites) is the read counterpart.
			b.Put("/devices/{id}/deployment",
				requireAuth(onboarded(staffScoped(devices.NewDeploymentPut(d.Registry, auditW)))))
			// PUT /devices/{id}/alpr-license — staff-only set of a device's
			// Plate Recognizer license (#84). Stored secret; pushed to the
			// device at Commission (#91), not here.
			b.Put("/devices/{id}/alpr-license",
				requireAuth(onboarded(staffScoped(devices.NewALPRLicensePut(d.Registry, auditW)))))
			// POST /devices/{id}/commission — staff-only Commission (#91).
			// Gated on a configured Commissioner (needs the Tailscale
			// credential + the cmd publisher).
			if d.Commissioner != nil {
				b.Post("/devices/{id}/commission",
					requireAuth(onboarded(staffScoped(devices.NewCommissionPost(d.Commissioner, auditW)))))
			}
		}
		// DELETE /devices/{id} — staff-only device decommission (CP-side row
		// removal; AWS IoT thing/cert teardown is out-of-band per the
		// decommission runbook). Audited; child rows cascade.
		b.Delete("/devices/{id}",
			requireAuth(onboarded(staffScoped(devices.NewDelete(d.Registry, auditW)))))
		// POST /agent-rollouts — staff-only agent fleet-update (#40):
		// stamp desired_agent_version on a target set + best-effort
		// initial agent.update push to the online targets. Scope is
		// required because target resolution reads the device list
		// (fail-closed without a SiteFilter in context). Skipped when
		// the agent-dist catalog/pusher aren't configured.
		if d.AgentRolloutCatalog != nil && d.AgentRolloutPusher != nil {
			b.Post("/agent-rollouts",
				requireAuth(onboarded(requireScope(requireStaff(
					devices.NewAgentRolloutPost(d.Registry, d.AgentRolloutCatalog, d.AgentRolloutPusher, auditW))))))
		}
	}
	return b
}

// NewRouter returns a ready-to-serve http.Handler with the cplog
// correlation+access-log middleware wrapped around every route, and the
// CORS middleware outside cplog so preflight responses are still logged
// (useful for diagnosing browser-side failures).
func NewRouter(d Deps) http.Handler {
	return middleware.Cors(d.CORSAllowedOrigins)(cplog.Middleware(d.Logger)(NewBuilderWith(d).Handler()))
}
