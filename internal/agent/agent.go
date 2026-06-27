package agent

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/emilejacobs/control-plane/internal/captureupload"
	"github.com/emilejacobs/control-plane/internal/dispatcher"
	"github.com/emilejacobs/control-plane/internal/handlers/agentupdate"
	"github.com/emilejacobs/control-plane/internal/handlers/cameras"
	"github.com/emilejacobs/control-plane/internal/handlers/camerasnapshot"
	commissionhandler "github.com/emilejacobs/control-plane/internal/handlers/commission"
	"github.com/emilejacobs/control-plane/internal/handlers/configbackfill"
	"github.com/emilejacobs/control-plane/internal/handlers/configupdate"
	"github.com/emilejacobs/control-plane/internal/handlers/heartbeat"
	"github.com/emilejacobs/control-plane/internal/handlers/logtail"
	"github.com/emilejacobs/control-plane/internal/handlers/networkscan"
	"github.com/emilejacobs/control-plane/internal/handlers/prconfigupdate"
	"github.com/emilejacobs/control-plane/internal/handlers/servicerestart"
	"github.com/emilejacobs/control-plane/internal/handlers/servicestatus"
	"github.com/emilejacobs/control-plane/internal/handlers/snapshotconfig"
	"github.com/emilejacobs/control-plane/internal/probes"
	"github.com/emilejacobs/control-plane/internal/protocol/cmdsign"
	protologtail "github.com/emilejacobs/control-plane/internal/protocol/logtail"
	"github.com/emilejacobs/control-plane/internal/protocol/upload"
	"github.com/emilejacobs/control-plane/internal/service"
	"github.com/emilejacobs/control-plane/internal/snapshotscheduler"
	"github.com/emilejacobs/control-plane/internal/snapshotstate"
	"github.com/emilejacobs/control-plane/internal/telemetry"
)

// defaultLogTailReader wraps PerOSAllowList + the kind-aware fetcher
// so the agent can register the log.tail handler without test-only
// injection. Tests pass a stub via WithLogTailReader.
//
// Kind dispatch (issue #7): "file" → TailFile; "docker" → TailDocker.
// Unknown kinds fall through with a clear CodeReadError so the
// dashboard surfaces drift rather than silently dropping the request.
type defaultLogTailReader struct{}

func (defaultLogTailReader) AllowList() map[string]protologtail.Entry { return PerOSAllowList() }
func (defaultLogTailReader) Tail(entry protologtail.Entry, lines int) (protologtail.Response, error) {
	switch entry.Kind {
	case protologtail.KindFile:
		return TailFile(entry.Target, lines, protologtail.MaxContentSize)
	case protologtail.KindDocker:
		return TailDocker(entry.Target, lines, protologtail.MaxContentSize)
	default:
		return protologtail.Response{}, &protologtail.ValidationError{
			Code:    protologtail.CodeReadError,
			Message: "unknown allow-list kind " + entry.Kind + " for " + entry.Name,
		}
	}
}

const (
	defaultTelemetryInterval     = 30 * time.Second
	defaultServiceStatusInterval = 5 * time.Minute
	defaultProbeInterval         = 5 * time.Minute
	defaultCameraProbeInterval   = 5 * time.Minute
	// The MQTT-session watchdog (#65) declares a wedge when no publish has
	// succeeded for staleAfter (~10 missed 30s heartbeats), checked every
	// interval.
	defaultWatchdogStaleAfter = 5 * time.Minute
	defaultWatchdogInterval   = time.Minute
)

type Config struct {
	CertPath          string
	DeviceID          string
	Version           string
	TelemetryInterval time.Duration

	// ServiceAllowList enables the Phase 2 service-status reporter when
	// non-empty. Names are launchd unit names on Mac, systemd unit names
	// on Linux. ServiceStatusInterval defaults to 5 minutes when unset.
	ServiceAllowList      []string
	ServiceStatusInterval time.Duration

	// ProbeInterval is the cadence for the Phase 2 fleet-health-probes
	// reporter (issue #19); it runs whenever a probe backend is supplied
	// via WithProbeBackend. Defaults to 5 minutes when unset.
	ProbeInterval time.Duration

	// CameraProbeInterval is the cadence for the camera-status RTSP
	// reachability reporter (#113); it runs whenever CamerasPath is set.
	// Defaults to 5 minutes when unset. Minutes-scale on purpose — the
	// probe is far cheaper than a live view but still shells ffmpeg per
	// camera, so the cadence stays slow.
	CameraProbeInterval time.Duration

	// WatchdogStaleAfter / WatchdogInterval tune the MQTT-session watchdog
	// (#65). If no publish succeeds within WatchdogStaleAfter, the agent
	// signals WedgeDetected so main exits and launchd restarts it with a
	// fresh transport. The watchdog only runs when the transport reports
	// liveness (LastPublishSuccess). Default 5m stale / 1m check.
	WatchdogStaleAfter time.Duration
	WatchdogInterval   time.Duration

	// ConfigPath is the absolute path to the agent's JSON config file
	// (the same file that produced this Config via config.Load). When
	// non-empty, the Phase 2 slice 2 config.update dispatcher handler
	// is registered, allowing CP to push allow-list + cadence overrides
	// down via the cmd channel. Empty disables the downward channel.
	ConfigPath string

	// CamerasPath is the absolute path to the agent-managed cameras
	// JSON file (the downstream copy of CP's cameras inventory pushed
	// via the cameras.update cmd, per ADR-030 § 1). When non-empty,
	// the agent registers the cameras.update handler; empty disables
	// it. Suggested default in the install module:
	// /var/uknomi/agent-state/cameras.json.
	CamerasPath string

	// SnapshotStatePath is the absolute path to the agent's snapshot
	// state file (#9): the persisted scheduled-snapshot cadence + per-
	// camera next-fire times. When non-empty the agent registers the
	// snapshot.config handler; the scheduler (a later slice) reads it.
	// Suggested: /var/uknomi/agent-state/snapshot-state.json.
	SnapshotStatePath string

	// UpdateDir is the resident wrapper's on-disk update root (the
	// wrapper exports it as AGENT_DIR; see scripts/uknomi-agent-supervisor.sh
	// and ADR-035 §3/§5). When non-empty the agent registers the
	// signature-gated agent.update handler and, once alive + controllable,
	// writes the `healthy` marker the wrapper promotes a candidate on.
	// Empty (most tests, non-wrapper runs) disables both.
	UpdateDir string

	// AutoLoginUser is the non-root user Colima runs as (#89, ADR-038). The
	// commission handler uses it to drive the ALPR container through the
	// per-user runner; empty leaves tailnet-only commission working and ALPR
	// unavailable.
	AutoLoginUser string
}

type Transport interface {
	Subscribe(topic string, handler func(topic string, payload []byte)) error
	Publish(topic string, payload []byte) error
	Close() error
}

type Agent struct {
	transport      Transport
	dispatcher     *dispatcher.Dispatcher
	deviceID       string
	version        string
	logger         *slog.Logger
	serviceBackend service.Backend
	logTailReader  logtail.Reader
	networkScanner networkscan.Scanner
	startTime      time.Time
	telemetry      *telemetry.Publisher
	// serviceStatus + serviceStatusCollector are set whenever a service
	// backend was supplied via WithServiceBackend (Phase 2 slice 2:
	// always constructed, even with an empty initial allow-list, so
	// config.update can hot-reload an allow-list onto an empty start).
	// Both stay nil when no backend is supplied — agent unit-tests that
	// don't need service surfaces leave them nil.
	serviceStatusCollector *telemetry.ServiceStatusCollector
	serviceStatus          *telemetry.ServiceStatusPublisher
	serviceStatusDone      chan struct{}

	// probeBackend + probePublisher are set whenever a probe backend was
	// supplied via WithProbeBackend (Phase 2 fleet health probes, #19).
	// Both stay nil otherwise.
	probeBackend   probes.Backend
	probePublisher *telemetry.ProbePublisher
	probeDone      chan struct{}

	// colimaKeeperDone tracks the Colima-liveness backstop loop (#172), run
	// whenever the probe backend can ensure Colima (the darwin backend).
	colimaKeeperDone chan struct{}

	// cameraReach + cameraStatus are set whenever CamerasPath is
	// configured (#113 camera-status probe). cameraReach defaults to the
	// ffmpeg reachability check; WithCameraReachability overrides it in
	// tests. Both stay nil when no cameras path is configured.
	cameraReach      telemetry.Reachability
	cameraStatus     *telemetry.CameraStatusPublisher
	cameraStatusDone chan struct{}

	// snapshotScheduler is the scheduled-snapshot loop (#9), set when both a
	// snapshot state path and a cameras path are configured. Nil otherwise.
	snapshotScheduler *snapshotscheduler.Scheduler
	schedulerDone     chan struct{}

	pubCancel context.CancelFunc
	pubDone   chan struct{}

	// updateDir / updateFetch back the agent.update handler (issue #39).
	// updateDir is empty unless the agent runs under the resident wrapper;
	// updateFetch defaults to an HTTP GET of the presigned URL (tests
	// override via WithUpdateFetcher).
	updateDir   string
	updateFetch agentupdate.Fetcher

	// restart is closed once when a staged update asks the agent to exit so
	// the wrapper can health-gate the candidate. main selects on it.
	restart     chan struct{}
	restartOnce sync.Once
	healthDone  chan struct{}

	// wedged is closed once when the transport watchdog (#65) confirms a dead
	// MQTT session. main selects on it and exits so launchd restarts the agent
	// with a fresh transport. watchdog* hold the tuned thresholds.
	wedged             chan struct{}
	wedgedOnce         sync.Once
	watchdogStaleAfter time.Duration
	watchdogInterval   time.Duration
	watchdogDone       chan struct{}
}

type Option func(*Agent)

func WithLogger(l *slog.Logger) Option {
	return func(a *Agent) { a.logger = l }
}

func WithProbeBackend(b probes.Backend) Option {
	return func(a *Agent) { a.probeBackend = b }
}

// WithCameraReachability overrides the camera-status RTSP reachability
// check (#113). Production defaults to newFFmpegReachability (shells a
// short ffmpeg probe); tests inject a fake so they can drive online/
// offline without real cameras or ffmpeg on PATH.
func WithCameraReachability(r telemetry.Reachability) Option {
	return func(a *Agent) { a.cameraReach = r }
}

func WithServiceBackend(b service.Backend) Option {
	return func(a *Agent) { a.serviceBackend = b }
}

// WithLogTailReader overrides the default file-reader the log.tail
// handler delegates to. Production wires defaultLogTailReader (which
// uses PerOSAllowList + TailFile); tests pass a stub so they can
// assert against in-memory paths.
func WithLogTailReader(r logtail.Reader) Option {
	return func(a *Agent) { a.logTailReader = r }
}

// WithNetworkScanner overrides the default LAN-scan implementation.
// Production wires newNmapScanner (which shells out to nmap); tests
// pass a fake so they can assert against canned hosts without root /
// nmap on PATH.
func WithNetworkScanner(s networkscan.Scanner) Option {
	return func(a *Agent) { a.networkScanner = s }
}

// WithUpdateFetcher overrides how the agent.update handler downloads a staged
// binary. Production wires an HTTP GET of CP's presigned URL; tests inject a
// stub so they can exercise staging without a real download.
func WithUpdateFetcher(f agentupdate.Fetcher) Option {
	return func(a *Agent) { a.updateFetch = f }
}

func New(cfg Config, transport Transport, opts ...Option) (*Agent, error) {
	if err := validateCertFile(cfg.CertPath); err != nil {
		return nil, err
	}

	a := &Agent{
		transport:          transport,
		deviceID:           cfg.DeviceID,
		version:            cfg.Version,
		logger:             slog.New(slog.NewJSONHandler(io.Discard, nil)),
		startTime:          time.Now(),
		updateDir:          cfg.UpdateDir,
		restart:            make(chan struct{}),
		wedged:             make(chan struct{}),
		watchdogStaleAfter: cfg.WatchdogStaleAfter,
		watchdogInterval:   cfg.WatchdogInterval,
	}
	if a.watchdogStaleAfter <= 0 {
		a.watchdogStaleAfter = defaultWatchdogStaleAfter
	}
	if a.watchdogInterval <= 0 {
		a.watchdogInterval = defaultWatchdogInterval
	}
	for _, opt := range opts {
		opt(a)
	}

	// The agent.update command is signature-gated (issue #41): when the
	// update surface is active, an agent.update must carry a valid command
	// signature or the dispatcher rejects it before the handler runs. Other
	// command types stay unsigned per ADR-028 forward-compat.
	dopts := []dispatcher.Option{dispatcher.WithLogger(a.logger)}
	if a.updateDir != "" {
		dopts = append(dopts, dispatcher.WithSignatureVerification(cmdsign.VerifyCommand, "agent.update"))
	}
	a.dispatcher = dispatcher.New(dopts...)
	a.dispatcher.Register("heartbeat", heartbeat.New(cfg.DeviceID, cfg.Version, a.startTime))
	if a.serviceBackend != nil {
		a.dispatcher.Register("service.status", servicestatus.New(a.serviceBackend))
		a.dispatcher.Register("service.restart", servicerestart.New(a.serviceBackend))
	}

	// Phase 2 slice 3: log.tail handler. Always registered when the
	// agent runs in production; tests can pass WithLogTailReader to
	// substitute a stub (or omit it to disable the handler entirely
	// for tests that don't care about the surface).
	if a.logTailReader == nil {
		a.logTailReader = defaultLogTailReader{}
	}
	a.dispatcher.Register("log.tail", logtail.New(a.logTailReader))

	// Commission handler (#91): join the tailnet with the minted key and, for
	// ALPR devices, start the Plate Recognizer container via the per-user
	// Colima runner. Always registered — every assigned device is commissioned.
	a.dispatcher.Register("commission", commissionhandler.New(newCommissionApplier(cfg.AutoLoginUser)))

	// pr.config.update (#5): merge CP-managed Plate Recognizer fields into the
	// on-disk config.ini and bounce the container via the Colima runner. Always
	// registered (ALPR-only devices receive it; others never get the cmd). The
	// stream dir is the same bind-mount the container module uses (ADR-038).
	a.dispatcher.Register("pr.config.update", prconfigupdate.New(
		newPRConfigApplier("/usr/local/etc/plate-recognizer/stream", cfg.AutoLoginUser)))

	// Phase 2 Edge UI rework (issue #2): cameras.update handler.
	// Registered when a cameras file path is configured. Empty path
	// disables the downward channel — tests that don't exercise
	// cameras leave it empty.
	if cfg.CamerasPath != "" {
		a.dispatcher.Register("cameras.update", cameras.New(NewCamerasApplier(cfg.CamerasPath)))
		// camera.snapshot (#8 Slice B): resolve the camera from the same local
		// cameras file, ffmpeg one frame, PUT to CP's presigned URL. Needs the
		// cameras file, so it shares the CamerasPath gate.
		a.dispatcher.Register("camera.snapshot", camerasnapshot.New(
			newCamerasFileReader(cfg.CamerasPath),
			newFFmpegSnapshotter(),
			newHTTPUploader(),
		))
	}

	// snapshot.config (#9): persist the per-device scheduled-snapshot cadence
	// to the snapshot state file. Registered when the state path is set.
	if cfg.SnapshotStatePath != "" {
		snapState := snapshotstate.NewStore(cfg.SnapshotStatePath)
		a.dispatcher.Register("snapshot.config", snapshotconfig.New(snapState))

		// Scheduled snapshots (#9 slice 3b): on the persisted cadence, capture
		// each camera and upload via the generic handshake. The uploader runs
		// in the scheduler goroutine (not a command handler), so it can await
		// the upload.url grant — which the dispatcher routes to HandleGrant.
		// Needs the cameras file to resolve RTSP URLs.
		if cfg.CamerasPath != "" {
			httpUp := newHTTPUploader()
			uploader := captureupload.New(cfg.DeviceID, a.transport.Publish, httpUp.Put)
			a.dispatcher.Register(upload.TypeURL, dispatcher.HandlerFunc(uploader.HandleGrant))
			a.snapshotScheduler = snapshotscheduler.New(
				newCamerasFileReader(cfg.CamerasPath),
				newFFmpegSnapshotter(),
				uploader,
				snapState,
				snapshotscheduler.WithLogger(a.logger),
			)
		}
	}

	// Phase 2 Edge UI rework (issue #3): network.scan handler.
	// Always registered in production (the agent ships with the nmap
	// scanner by default). Tests that don't exercise the surface omit
	// it by leaving the default in place and not sending the cmd.
	if a.networkScanner == nil {
		a.networkScanner = newNmapScanner()
	}
	a.dispatcher.Register("network.scan", networkscan.New(a.networkScanner))

	// Agent fleet-update handler (issue #39, ADR-035 §3). Registered only
	// when running under the resident wrapper (UpdateDir set): it verifies
	// the signed manifest, refuses downgrades (cfg.Version is the running
	// version), fetches + stages the candidate, then asks the agent to exit
	// so the wrapper health-gates it. The envelope signature is already
	// enforced by the dispatcher gate wired above.
	if a.updateDir != "" {
		if a.updateFetch == nil {
			a.updateFetch = httpUpdateFetch
		}
		a.dispatcher.Register("agent.update",
			agentupdate.New(a.updateDir, a.updateFetch, a.requestRestart, cfg.Version))
	}

	interval := cfg.TelemetryInterval
	if interval <= 0 {
		interval = defaultTelemetryInterval
	}
	a.telemetry = &telemetry.Publisher{
		Interval:   interval,
		DeviceID:   cfg.DeviceID,
		Collectors: a.defaultCollectors(),
		Transport:  transport,
		Logger:     a.logger,
	}

	// Phase 2: service-status publisher. Constructed whenever the
	// agent has a service backend, even if the initial allow-list is
	// empty — slice 2 needs the publisher running so config.update can
	// hot-reload an allow-list onto an agent that started empty.
	// Empty-list ticks produce empty Reports which cp-ingest's
	// RecordServiceStates treats as a no-op, so the operational cost
	// is a harmless 5-min heartbeat-shaped publish.
	if a.serviceBackend != nil {
		ssInterval := cfg.ServiceStatusInterval
		if ssInterval <= 0 {
			ssInterval = defaultServiceStatusInterval
		}
		a.serviceStatusCollector = &telemetry.ServiceStatusCollector{
			Backend:   a.serviceBackend,
			DeviceID:  cfg.DeviceID,
			AllowList: cfg.ServiceAllowList,
			Now:       time.Now,
			Logger:    a.logger,
		}
		a.serviceStatus = &telemetry.ServiceStatusPublisher{
			Interval:  ssInterval,
			DeviceID:  cfg.DeviceID,
			Collect:   a.serviceStatusCollector.Collect,
			Transport: transport,
			Logger:    a.logger,
		}

		// Phase 2 slice 2: register the config.update handler when a
		// config path was supplied. The Applier persists the override
		// to disk and hot-reloads the collector + publisher (ADR-028).
		if cfg.ConfigPath != "" {
			applier := NewConfigUpdateApplier(cfg.ConfigPath, a.serviceStatusCollector, a.serviceStatus)
			a.dispatcher.Register("config.update", configupdate.New(applier))
			// config.backfill (#85): persist install-time-only fields
			// (snapshot_state_path) delivered to a device whose config predates
			// them; takes effect on the next restart.
			a.dispatcher.Register("config.backfill", configbackfill.New(NewConfigBackfillApplier(cfg.ConfigPath)))
		}
	}

	// Phase 2 fleet health probes (issue #19). Constructed whenever a
	// probe backend was supplied; the darwin backend ships in slice 1,
	// the linux backend returns an empty result set (ADR-007).
	if a.probeBackend != nil {
		probeInterval := cfg.ProbeInterval
		if probeInterval <= 0 {
			probeInterval = defaultProbeInterval
		}
		collector := &telemetry.ProbeCollector{
			Backend:  a.probeBackend,
			DeviceID: cfg.DeviceID,
			Now:      time.Now,
			Logger:   a.logger,
		}
		a.probePublisher = &telemetry.ProbePublisher{
			Interval:  probeInterval,
			DeviceID:  cfg.DeviceID,
			Collect:   collector.Collect,
			Transport: transport,
			Logger:    a.logger,
		}
	}

	// Camera-status probe (#113). Runs whenever a cameras path is
	// configured (the same gate as cameras.update / the snapshot
	// scheduler) — it reads the same agent-managed cameras.json to know
	// which cameras to probe. Reachability defaults to the ffmpeg check
	// unless a test injected one via WithCameraReachability.
	if cfg.CamerasPath != "" {
		if a.cameraReach == nil {
			a.cameraReach = newFFmpegReachability()
		}
		cameraInterval := cfg.CameraProbeInterval
		if cameraInterval <= 0 {
			cameraInterval = defaultCameraProbeInterval
		}
		camCollector := &telemetry.CameraStatusCollector{
			DeviceID: cfg.DeviceID,
			Cameras:  newCamerasFileReader(cfg.CamerasPath).Cameras,
			Reach:    a.cameraReach,
			Now:      time.Now,
			Logger:   a.logger,
		}
		a.cameraStatus = &telemetry.CameraStatusPublisher{
			Interval:  cameraInterval,
			DeviceID:  cfg.DeviceID,
			Collect:   camCollector.Collect,
			Transport: transport,
			Logger:    a.logger,
		}
	}

	return a, nil
}

func (a *Agent) defaultCollectors() []func() map[string]any {
	return []func() map[string]any{
		func() map[string]any {
			return map[string]any{
				"device_id":      a.deviceID,
				"version":        a.version,
				"os":             runtime.GOOS,
				"uptime_seconds": int64(time.Since(a.startTime).Seconds()),
			}
		},
		func() map[string]any {
			last := a.dispatcher.LastCommandAt()
			if last.IsZero() {
				return map[string]any{"last_command_at": nil}
			}
			return map[string]any{"last_command_at": last.UTC().Format(time.RFC3339)}
		},
		// Issue #14: publish the device's primary RFC1918 IPv4
		// (lan_ip), Tailscale IPv4 (tailscale_ip), and MagicDNS
		// name (tailscale_name) when each is detected. Fields are
		// OMITTED (not "") when their detector returns empty so
		// cp-ingest's conditional UPDATE doesn't clobber stored
		// values when the agent loses tailnet visibility mid-life.
		NewNetworkCollector(SystemInterfaceAddrs{}, SystemTailscaleStatusRunner{}),
		// Rollout rollback signal (#42 follow-up): when the resident wrapper
		// has reverted a failed candidate it appends the version to
		// <UpdateDir>/rollback.log. Report the most recent one so CP can show
		// "rolled_back" instead of an indefinite "in_flight". OMITTED when
		// there's nothing to report (no UpdateDir, or no rollback yet).
		func() map[string]any {
			if v := a.rolledBackVersion(); v != "" {
				return map[string]any{"rolled_back_version": v}
			}
			return map[string]any{}
		},
		// Offline-reason signal (#157): system boot_time + previous-shutdown
		// cause, read once at start (cached). CP infers reboot-vs-blip from
		// boot_time deltas. Omitted on non-macOS or read failure.
		newBootInfoCollector(readBootInfo()),
	}
}

// rolledBackVersion returns the version the resident wrapper most recently
// reverted — the last line of <UpdateDir>/rollback.log — or "" when the agent
// isn't running under the wrapper or no rollback has been recorded.
func (a *Agent) rolledBackVersion() string {
	if a.updateDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(a.updateDir, "rollback.log"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (a *Agent) Start() error {
	cmdTopic := "devices/" + a.deviceID + "/cmd"
	resultTopic := "devices/" + a.deviceID + "/cmd-result"

	if err := a.transport.Subscribe(cmdTopic, func(_ string, payload []byte) {
		resultBytes, err := a.dispatcher.Dispatch(context.Background(), payload)
		if err != nil {
			a.logger.Error("dispatch failed", "error", err)
			return
		}
		if err := a.transport.Publish(resultTopic, resultBytes); err != nil {
			a.logger.Error("publish result failed", "error", err)
		}
	}); err != nil {
		return err
	}

	pubCtx, cancel := context.WithCancel(context.Background())
	a.pubCancel = cancel
	a.pubDone = make(chan struct{})
	go func() {
		defer close(a.pubDone)
		a.telemetry.Run(pubCtx)
	}()

	if a.serviceStatus != nil {
		a.serviceStatusDone = make(chan struct{})
		go func() {
			defer close(a.serviceStatusDone)
			a.serviceStatus.Run(pubCtx)
		}()
	}
	if a.probePublisher != nil {
		a.probeDone = make(chan struct{})
		go func() {
			defer close(a.probeDone)
			a.probePublisher.Run(pubCtx)
		}()
	}
	// Colima-liveness backstop (#172): if the probe backend can ensure Colima
	// (the darwin backend), keep the VM up — the user LaunchAgent loads
	// unreliably at boot. Other OSes don't implement it, so this never runs.
	if ce, ok := a.probeBackend.(probes.ColimaEnsurer); ok {
		a.colimaKeeperDone = make(chan struct{})
		go func() {
			defer close(a.colimaKeeperDone)
			a.runColimaKeeper(pubCtx, ce, colimaEnsureInterval)
		}()
	}
	if a.cameraStatus != nil {
		a.cameraStatusDone = make(chan struct{})
		go func() {
			defer close(a.cameraStatusDone)
			a.cameraStatus.Run(pubCtx)
		}()
	}

	// Scheduled-snapshot loop (#9 slice 3b). Runs under pubCtx so Stop cancels it.
	if a.snapshotScheduler != nil {
		a.schedulerDone = make(chan struct{})
		go func() {
			defer close(a.schedulerDone)
			a.snapshotScheduler.Run(pubCtx)
		}()
	}

	// MQTT-session watchdog (#65). Only runs when the transport reports
	// liveness — the production transport does; bare test fakes don't, so they
	// see no behaviour change. On a confirmed wedge it signals WedgeDetected
	// (main exits → launchd restarts with a fresh transport).
	if lr, ok := a.transport.(livenessReporter); ok {
		a.watchdogDone = make(chan struct{})
		wd := &watchdog{
			lastSuccess:   lr.LastPublishSuccess,
			staleAfter:    a.watchdogStaleAfter,
			checkInterval: a.watchdogInterval,
			onWedged:      a.signalWedged,
			logger:        a.logger,
		}
		go func() {
			defer close(a.watchdogDone)
			wd.run(pubCtx)
		}()
	}

	// Update health marker (issue #39, ADR-035 §5). The Subscribe above
	// proves mTLS-connected + cmd-topic-subscribed (controllable); one
	// heartbeat proves alive. Emit that heartbeat now, then write
	// <UpdateDir>/healthy = version so the resident wrapper promotes the
	// candidate it's gating. Marker failure is logged, not fatal.
	if a.updateDir != "" {
		a.healthDone = make(chan struct{})
		go func() {
			defer close(a.healthDone)
			a.telemetry.PublishOnce()
			path := filepath.Join(a.updateDir, "healthy")
			if err := os.WriteFile(path, []byte(a.version), 0o644); err != nil {
				a.logger.Error("write health marker failed", "error", err, "path", path)
				return
			}
			a.logger.Info("health marker written", "version", a.version, "path", path)
		}()
	}
	return nil
}

// requestRestart asks the process to exit so the resident wrapper can
// health-gate a freshly-staged update candidate. It is the agentupdate
// handler's OnStaged callback. Idempotent: a second staged update in the
// same lifetime is a no-op (the first restart is already in flight).
func (a *Agent) requestRestart() {
	a.restartOnce.Do(func() {
		a.logger.Info("update staged — requesting restart for the wrapper to gate it")
		close(a.restart)
	})
}

// RestartRequested fires when a staged update wants the agent to exit (so the
// resident wrapper restarts and health-gates the candidate). main selects on
// it alongside SIGTERM/SIGINT and exits 0.
func (a *Agent) RestartRequested() <-chan struct{} { return a.restart }

// livenessReporter is the optional transport capability the watchdog needs:
// the time of the last successful publish. *transport.Transport implements it.
type livenessReporter interface {
	LastPublishSuccess() time.Time
}

// signalWedged is the watchdog's onWedged callback — closes wedged once.
func (a *Agent) signalWedged() {
	a.wedgedOnce.Do(func() { close(a.wedged) })
}

// WedgeDetected fires when the transport watchdog confirms a dead MQTT session
// (#65). main selects on it and exits non-zero so launchd restarts the agent
// with a fresh transport — the proven recovery for a wedged session.
func (a *Agent) WedgeDetected() <-chan struct{} { return a.wedged }

// httpUpdateFetch downloads a staged binary from CP's presigned URL. The
// agentupdate handler re-checks the sha256 against the signed manifest, so a
// tampered URL yields bytes that fail that check.
func httpUpdateFetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (a *Agent) Stop() error {
	if a.pubCancel != nil {
		a.pubCancel()
		<-a.pubDone
		if a.serviceStatusDone != nil {
			<-a.serviceStatusDone
		}
		if a.probeDone != nil {
			<-a.probeDone
		}
		if a.colimaKeeperDone != nil {
			<-a.colimaKeeperDone
		}
		if a.cameraStatusDone != nil {
			<-a.cameraStatusDone
		}
		if a.schedulerDone != nil {
			<-a.schedulerDone
		}
		if a.watchdogDone != nil {
			<-a.watchdogDone
		}
	}
	if a.healthDone != nil {
		<-a.healthDone
	}
	return a.transport.Close()
}

func validateCertFile(path string) error {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cert file %s: %w", path, err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return fmt.Errorf("cert file %s: not a valid PEM block", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("cert file %s: %w", path, err)
	}
	if time.Now().After(cert.NotAfter) {
		return fmt.Errorf("cert file %s: expired at %s", path, cert.NotAfter.Format(time.RFC3339))
	}
	return nil
}
