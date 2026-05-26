# ADR-030: Edge UI per-feature surface model

**Status:** Accepted (2026-05-25)

**Context.**

[ADR-029](./0029-edge-ui-rework-scope.md) committed Edge UI to a rewrite onto the CP stack with CP as source of truth, but explicitly deferred the per-feature decisions — which features survive in Edge UI, which move to CP, which drop entirely, and the new patterns those decisions introduce — to a follow-up grilling session.

This ADR captures the output of that grilling (2026-05-25). It is the operational complement to ADR-029's scope decision: ADR-029 said "rewrite onto CP stack"; this ADR says exactly what gets built on each side of the CP/Edge boundary.

The grilling was guided by the operator's actual-use list from the planning session: network IP scan for camera setup; cameras CRUD + label + angle verification; Plate Recognizer config (LPR camera + region + webhooks → docker restart); device info; service status + restart; service logs + Plate Recognizer docker logs; test audio recording (currently broken, see § 6). Surfaces that the operator does not use today — the setup wizard, the reverse-proxy network browser, the top-level dashboard — were already dropped by ADR-029 and are not revisited here.

**Decisions.**

### 1. Cameras

CP owns the cameras inventory; Edge UI exists only as a deep-link target for live RTSP preview.

- **CP**: per-device Cameras panel does full CRUD (add/edit/delete/label/LPR-flag/RTSP URL). Schema: per-device camera rows with `is_lpr` flag. Fleet-wide queries become possible ("show every LPR camera at every site").
- **Sync to device**: new agent cmd `cameras.update` follows the [ADR-028](./0028-unsigned-config-update-phase-2.md) pattern — full-list payload, agent atomically writes a local cameras file, ACKs on `cmd-result` with `last_applied_at` + `last_applied_correlation_id`. The agent owns the on-disk file; Edge UI just reads it.
- **Live preview** (camera angle verification): CP's "Verify angle" button opens a new tab at `https://<device-hostname>.<tailnet>/preview/<camera_id>`. Edge UI serves an MJPEG stream produced by an on-device `ffmpeg -rtsp_transport tcp -i <url> -f mjpeg ...` pipe. Tailnet membership is the auth; no Basic auth, no token gate.
- **LAN IP fallback**: if tailnet is unreachable for some reason, CP surfaces the device's last-known LAN IP as a copy-paste hint ("can't reach over tailnet? Try http://192.168.x.y:port/preview/<cam_id>"). Hint only — not a feature with retry semantics.

**Alternatives rejected:** (a) Edge UI keeping camera CRUD with CP as read-only mirror — creates two write paths and drift. (b) Split-by-capability where CP owns inventory and Edge UI owns RTSP URL discovery — contract boundary is confusing for operators. (c) Reverse-proxy live preview through CP — bandwidth/latency hit; and we just deleted Edge UI's reverse proxy explicitly because it never worked.

### 2. Network scan

New agent cmd `network.scan`, operator-triggered from CP.

- CP's Cameras panel has a "Scan network" button. Click → CP sends `network.scan` cmd → agent runs the LAN scan (the current `modules/07-camera-scan.sh` becomes the agent's payload, or its rewrite if cleaner) → results return on `cmd-result` → CP displays the candidate IPs.
- **Result shape**: structured JSON, not text. Each candidate includes `ip`, `mac`, `vendor` (resolved from OUI lookup table), `open_ports` (filtered to typical camera ports — 80, 443, 554, 8000, 8080).
- **Pattern parity**: same dispatcher / cmd-result ACK / audit row / Phase 3 signed-envelope absorption as the other unsigned handlers.

**Alternatives rejected:** (a) Scheduled scan with results in heartbeat metadata — staleness defeats the install workflow (operator wants current results). (b) Operator-side scan tool (nmap on operator's laptop) — strips a real workflow tool and assumes laptop is on the right LAN.

### 3. Plate Recognizer config

New agent cmd `pr.config.update`, structured payload. CP owns the config; agent applies + restarts container.

- **Wire payload** (concrete shape — implementation detail-level documented for the slice PRD):

```json
{
  "camera_id": "0",
  "region": "us-tx",
  "caching": false,
  "image": true,
  "lpr_camera_rtsp_url": "rtsp://user:pass@192.168.1.42:554/stream",
  "webhooks": [
    {"name": "transcriber-prod", "url": "https://...", "enabled": true},
    {"name": "audit-prod",       "url": "https://...", "enabled": false}
  ]
}
```

- **LPR camera URL** lives in the payload directly — CP resolves "which camera has `is_lpr=true`" against its own DB and ships the resolved URL. Agent does no lookup; writes what it's told to `config.ini`. Decouples PR config apply timing from cameras-update timing.
- **`region`**: editable dropdown of valid PR region codes. **`image`**: defaults to `true` for new devices.
- **Webhooks**: URLs come from a CP-wide endpoint registry — see [ADR-031](./0031-webhook-endpoint-registry.md). Per-device PR config stores *which named endpoints are enabled*; CP resolves names → URLs at push time. Agent never sees the registry, only resolved URLs.
- **Apply flow** (agent-side): write `config.ini` + `webhooks-meta.json` → `docker cp` → `docker restart plate-recognizer-stream` → ACK on `cmd-result` with applied-at stamp.
- **Field whitelist hygiene**: agent-side handler rejects any field outside this exact shape. Same protective stance as ADR-028's `config.update` whitelist.

**Alternatives rejected:** (a) Extending the existing slice-2 `config.update` cmd with PR fields — would require ADR-028 amendment and muddies the handler. (b) Folding PR config into `cameras.update` — couples two concepts with independent lifecycles.

### 4. Service control: drop Edge UI's panel entirely

Edge UI no longer has a services page. Service status read is already CP-side (Phase 2 slice 1); signed `service.restart` write lands in Phase 3 per ADR-013.

- **Why total drop, not offline-fallback**: Phase 3's signed-restart pipeline is committed. Building an offline fallback in Edge UI for the rare CP-outage case adds maintenance burden for negligible operator benefit.
- **Service list shrinkage**: the original 5 managed services were Tailscale, AnyDesk, Zabbix, Plate Recognizer, Edge UI-itself. Per [fleet_software_deprecations] memory, Zabbix + AnyDesk are being phased out. Edge UI managing Edge UI is self-referential. Effectively that leaves Tailscale + Plate Recognizer + the agent. All three are already in CP's service-status pipeline; no new CP work needed.
- **Phase 3 follow-on**: the agent's `service.Backend` is launchctl-only on macOS today. Tailscale (`brew services`) and Plate Recognizer (`docker`) need backend handling for the signed-restart capability to apply to them. Flagged as a Phase 3 issue, not a Phase 2/Edge-UI-rework issue.

### 5. Logs: drop Edge UI's page, extend `log.tail` with a `docker` kind

Edge UI's `/logs` page does not survive. Phase 2 slice 3's `log.tail` mechanism handles everything.

- **Agent allow-list extension**: each entry gains a `kind` discriminator — `file` (today's default) or `docker`. Entry shape becomes `{name, kind, target, label}`. The on-wire `log.tail` cmd stays unchanged (sends `name` + `lines`); the agent's resolver picks the fetch method.
- **Default Mac allow-list**: adds one new entry — `{name: "plate-recognizer", kind: "docker", target: "plate-recognizer-stream", label: "Plate Recognizer (Docker)"}`. The 7 file-based entries stay.
- **Per-device override** for the log allow-list — same pattern as slice 2's allow-list override — is a deferred Phase 2 followup (not in this ADR's scope).
- **Docker socket access**: agent runs as root via LaunchDaemon; Docker Desktop's socket is accessible. Linux path depends on socket permissions on the target distro — document, not architect.

### 6. Audio test

Stays local in Edge UI. Hardware-bound (microphone, TCC permissions). Three sub-decisions:

- **Scope**: record + play + transcribe. Whisper model bundled with the new Edge UI install (~750 MB quantized medium-en, acceptable). Transcription stays because the *install-time audio QA workflow is load-bearing* — the on-site tech runs a test transcribe to confirm audio quality is good enough for the production Transcriber service to produce useful output. Skipping this step has historically produced bad transcripts in production.
- **3× playback bug**: fixed as part of the rewrite, not in the Flask app. Acceptance criterion on the audio-test slice: recorded WAV plays back at correct speed and duration. Root cause is almost certainly an unpinned `ffmpeg` sample rate vs WAV header mismatch — concrete fix is to pin `-ar` and ensure header matches data.
- **Entry point**: CP deep-links from the device's Diagnostics surface → Edge UI's audio page in a new tab. Same pattern as camera live preview.
- **Deferred nice-to-have**: audio-level diagnostics (RMS / peak / clipping detection) giving the on-site tech a "too soft" / "too loud" indicator. Tractable in the rewrite (a few lines using `audioop` or equivalent). Not blocking — followup issue.

### 7. Captures pipeline (NEW pattern)

This is the first device-produced-binary-artifact pipeline in CP. Snapshots, audio recordings, and transcripts share one model: **device captures → S3 → CP indexes**.

- **Upload mechanism**: CP-vended pre-signed PUT URLs. Agent publishes `upload.request` (kind, size, content-type, metadata); CP responds with a signed S3 PUT URL over the cmd channel; agent does the HTTP PUT to S3 directly; agent publishes `upload.complete` with the S3 key. CP indexes the row.
- **Edge UI → agent handoff for recordings**: Edge UI captures to a known local directory (e.g., `/var/tmp/uknomi-edge/audio-test/`); agent uses `fsnotify` to detect new files and triggers the upload pipeline. Edge UI knows nothing about S3, CP, or upload. One-way information flow.
- **Snapshot triggers**: scheduled (weekly default; agent has a ticker goroutine) **plus** on-demand from CP (new `camera.snapshot` cmd, per-camera button in CP's Cameras panel).
- **Recording triggers**: on-demand only, operator initiates from Edge UI's audio test page (typically during install).
- **S3 layout**: `s3://uknomi-cp-captures/<kind>/<device_id>/<timestamp>.<ext>` where `<kind>` is `snapshots` / `audio` / `transcripts`. Separate top-level prefixes so lifecycle policy can target snapshots only.
- **CP DB**: one table `device_captures` (polymorphic, with a `kind` column) rather than three separate tables — better for fleet-view queries and pagination. Schema details for the slice PRD.
- **CP surfacing**:
  - Cameras panel rows show latest snapshot thumbnail; click → full-size + per-camera snapshot timeline.
  - Per-device Diagnostics panel shows audio recordings list — timestamp, duration, play button (CP streams from signed S3 GET), transcript text, share button (copies a 7-day signed URL).
- **Retention**:
  - **Snapshots**: 90-day S3 lifecycle policy auto-expires older objects.
  - **Audio recordings + transcripts**: no retention policy. Forever. Cost is negligible (~$0.30/year/device at realistic usage) and the "share with others 6 months later" use case is real.
- **Out of scope**: fleet-wide camera gallery view (operator explicitly de-scoped).

### 8. The resulting Edge UI surface

Exactly two pages in the new Edge UI:

1. **Camera live preview** at `/preview/<camera_id>` — MJPEG stream from on-device ffmpeg pipe
2. **Audio test** at `/audio` — record + play + transcribe

No auth (tailnet-as-perimeter). No persistent state owned by Edge UI. No CP credentials in Edge UI. No services panel, no logs page, no cameras CRUD, no PR config editor, no setup wizard, no dashboard, no reverse proxy.

Install footprint: a Next.js app served by a small Go binary (or Node, if pragma argues for it), launched by a substantially simpler launchd plist than today. Python venv + Flask + Keychain entry + WEBUI_PASSWORD env var all go away.

### 9. New agent cmds introduced

Enumerated for stability:

- `cameras.update` — full cameras list, atomic local write, ACK
- `network.scan` — operator-triggered LAN scan, structured results
- `pr.config.update` — PR config object, write + restart container, ACK
- `camera.snapshot` — on-demand snapshot of one camera, triggers capture pipeline
- `upload.request` / `upload.complete` — capture-pipeline helpers (CP vends signed URL)

All five follow ADR-028's "unsigned in Phase 2, signed envelope in Phase 3" pattern. Phase 3's signed-cmd work absorbs them along with the existing handlers.

### 10. CONTEXT.md updates

New terms: *Capture*, *Snapshot*, *Audio recording*, *Transcript*, *Webhook endpoint registry*, *Camera live preview*, *Network scan*. Updated definition of *Edge UI* (narrower surface per this ADR). Captured inline.

**Out of scope for this ADR (deferred elsewhere).**

- **Setup process redesign** — phases, Terminal vs CP-pushed, asset-number auto-assignment from CP, Mosyle relationship, cross-OS install module shape. Subject of a separate grilling session (own ADR when settled). Tied into [ADR-029] but big enough to deserve dedicated treatment.
- **Audio-level diagnostics** (RMS / peak / clipping). Deferred nice-to-have. Followup issue at audio-test slice time.
- **Per-device log allow-list override**. Phase 2 followup `phase-2-followups/`.
- **Fleet camera gallery view**. Dropped (not deferred).
- **Webhook endpoint registry pattern details**. See sibling [ADR-031](./0031-webhook-endpoint-registry.md).

**Consequences.**

- (+) The CP/Edge boundary is no longer fuzzy. Every operator-facing capability has a clear home. No "edit cameras in either place" drift risk.
- (+) The new Edge UI surface is ~10× smaller than today's (30 routes → 2 pages). Maintenance burden drops sharply; rewrite cost is bounded.
- (+) Captures pipeline establishes the device-to-S3 pattern. Future surfaces (PR debug captures, screen recordings, anything else) reuse it without reinvention.
- (+) Five new agent cmds extend the established `config.update`-style pattern. No new dispatch shape, no new envelope, no new ACK semantics. Phase 3 signing absorbs the lot.
- (+) Operator workflow during install is materially cleaner: one CP dashboard with grouped panels instead of multiple Edge UI tabs.
- (-) Coordinated rollout required. The new Edge UI's first slice must land *before* CP's cameras CRUD is wired (otherwise operators have no live-preview path during transition). Slice sequencing matters.
- (-) The captures pipeline is non-trivial to build first time. Pre-signed URL plumbing, fsnotify watcher on the agent, new S3 bucket, new CP table, new dashboard surfaces. Single most expensive item in the rework.
- (-) The agent's responsibilities grow: weekly snapshot scheduler, file watcher, HTTP PUT to S3, new cmd handlers. Not breaking, but the agent process becomes meaningfully larger.
- (-) Operator's laptop now needs three network reachabilities simultaneously (store LAN + internet + tailnet) for the full install workflow. Today they only needed LAN. In practice this is already true (laptop on store wifi with Tailscale client); flagging as new requirement.
- (-) The Phase 3 agent service-backend extension (brew + docker beyond launchctl) becomes a hard dependency for service-restart on Tailscale or Plate Recognizer. Adds scope to Phase 3.

**Verification.**

- New agent cmds: each gets handler-level tests + an integration test asserting end-to-end cmd → ACK flow. Same pattern as `config.update` / `log.tail` test files.
- Captures pipeline: integration test asserts `upload.request` → signed URL response → S3 object exists with correct prefix → `upload.complete` → CP table row exists with matching key. Lifecycle policy verified via terraform plan inspection.
- Cameras CP-primary: dashboard test asserts edits in CP appear in agent's local cameras file after a single `cmd-result` ACK.
- Field whitelist hygiene (cmds 1–4): each handler has a "rejects unknown field" test, identical to ADR-028's verification.
- Webhook resolve-at-push-time: see ADR-031.
- The new Edge UI surface: explicit acceptance criteria on the rewrite slice — exactly two endpoints exist; the binary listens only on tailnet/loopback interface; no auth middleware in the request path; no Whisper / ffmpeg / docker invocations on any path other than `/audio` and `/preview/*`.
- Audio test sample-rate bug: WAV produced by the new audio-test endpoint plays at correct speed/duration on standard browsers; assertion in the slice's integration test (record a sine wave of known frequency, assert FFT peak matches expected).
