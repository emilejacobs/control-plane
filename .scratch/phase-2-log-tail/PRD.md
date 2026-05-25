# PRD — Log tail (on-demand)

**Phase:** 2 (third slice — after [service-status](../phase-2-service-status/PRD.md) + [allow-list overrides](../phase-2-allow-list-overrides/PRD.md))
**Started:** 2026-05-24
**Status:** in design

## Why

Slice 1's Services panel answers "is service X up?" Slice 2 lets the operator decide *which* services to track. Neither answers **"why is service X stopped?"** — for that, an operator today SSHes in and reads `/var/log/uknomi-agent.log`, `/var/log/install.log`, or the Edge UI's log files. Phase 2's success criterion (per [roadmap.md](../../docs/roadmap.md)) is "operators no longer SSH for read-only checks". Log tail is the keystone of that gate.

This slice **reuses the downward cmd channel slice 2 established** — `devices/{id}/cmd` → agent dispatcher → `devices/{id}/cmd-result` → cp-ingest. The fourth slot in the dispatcher (after `heartbeat`, `service.status`, `service.restart`, `config.update`) is `log.tail`. No new transport plumbing; just one new cmd type + one new ingest router + one new dashboard surface.

The `envelope.Result.Type` field added in slice 2 cycle 9 is what makes routing the response cheap: cp-ingest's `CmdResultIngester` adds an `if r.Type == "log.tail"` branch alongside the existing `config.update` one.

## Scope of this PRD

Just on-demand log tail: operator requests "give me the last N lines of file F on device D", agent returns one shot. Live streaming (`tail -f`) is **out**. Log file path globs / per-line filters are **out**. Search is **out** (operator pipes through `grep` locally after fetch).

## The shape

```
operator clicks "Logs" tab on device page → picks a log from the device's allow-list
  → POST /devices/{id}/logs/tail  {log_name, lines}        # auth + scope
  → CP persists request row (device_log_tails) keyed by correlation_id
  → CP publishes `log.tail` cmd on devices/{id}/cmd
  → agent reads the file (path resolved from per-OS allow-list)
  → returns content in the cmd-result envelope (capped to fit MQTT)
  → cp-ingest's CmdResultIngester routes Type=="log.tail" → updates the row
  → dashboard polls GET /devices/{id}/logs/tail/{correlation_id} until content lands
  → render the lines
```

Same async-via-poll pattern slice 2 established (operator-PUT → cmd → ACK → cp writes → dashboard polls). The latency tradeoff vs synchronous-via-WebSocket isn't worth the new transport channel; polling is "fetch happens within ~2s" which is fine for log fetching.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Response model | **Async-via-poll** (slice 2's pattern, persisted in Postgres keyed by correlation_id) | Synchronous needs cp-api to consume cmd-result topic (a new transport role) and an in-memory correlation cache (lost on cp-api restart). Polling is consistent with slice 2 and ~2s latency. |
| Storage | **New `device_log_tails` table**, PK `correlation_id`, columns `(device_id, log_name, lines_requested, status, content, error_code, requested_at, returned_at)` | Per-request row with TTL cleanup — easier than JSONB-on-devices, supports concurrent requests cleanly, makes the audit log "operator A read log X at time Y" trivial. |
| MQTT payload cap | **Content capped at 200 KB** in the cmd-result envelope | AWS IoT MQTT message limit is 256 KB; leaving 50 KB headroom for the envelope + correlation_id + path metadata. ~200 lines at 1 KB/line is the practical ceiling. Agent reports `truncated: true` when it had to cap. |
| Lines cap | **N ≤ 500 requested, server enforces** | UX: a "tail -500" is plenty for SSH-replacement; longer asks should chain (fetch older content via line offset — Phase 4 nicety). |
| Path resolution | **Per-OS allow-list bundled in the agent** | Same model as slice 1's `service_allow_list` (Mac: `/var/log/uknomi-agent.log`, `/var/log/uknomi-agent-error.log`, `/var/log/install.log`, `/usr/local/uknomi/<edge-ui logs>`; per-device override is a future slice). Agent refuses any path not in its list — operators can't request arbitrary files. |
| Binary refusal | **Heuristic check (>5% non-printable) → refuse** | Operator gets a clear error rather than garbled binary in the dashboard. Override knob deferred. |
| Encoding | **UTF-8 with replacement for invalid sequences** | Log files often have mixed encodings (locale-dependent timestamps, occasional non-ASCII). Don't fail the request on a single bad byte. |
| Auth | **Existing JWT + site-scope middleware** | Same as the PUT endpoint in slice 2. |
| Audit | **Every tail request audited** (operator_id, device_id, log_name, lines, success) | Phase 2's audit log already takes mutating endpoints; the read here is "operator-initiated remote-file-read of bounded scope", so it counts. |
| Rate limit | **Per-operator: 30 tail requests/min** | Bounds runaway dashboards / scripts. Same `middleware.NewRateLimiter` pattern enrollment uses. |
| Dashboard surface | **Card on the per-device page** with a log-name dropdown, lines input, and "Fetch" button. Below: a `<pre>` with the returned content. | Inline (not modal) — operators stay on the device page reading logs. Tabbed if multiple devices is ever a thing (it isn't in Phase 2). |

## Out of scope (later slices or phases)

- **Live `tail -f`** — would need WebSocket / server-sent events to push chunks. Phase 4 if anyone asks.
- **Cross-device log search** — "find this error across the fleet". Different problem (probably CloudWatch Logs Insights territory once agents ship their logs to CWLog Streams, not the per-fetch path).
- **Per-line filtering / `grep` server-side** — let operators pipe locally after fetch.
- **Pagination / line offset** — Phase 4 nicety. Slice 3's 500-line cap is generous enough for SSH-replacement.
- **Per-device log-path override** — same pattern as slice 2's allow-list override but for `log_allow_list`. Defer until at least one device needs a non-default path.
- **Compression** — text content compresses ~5–10×; could bump effective payload to ~1 MB-equivalent. Defer until 200 KB ceiling actually pinches.

## Constraints / honoured ADRs

- **ADR-011** (correlation IDs): the cmd's `correlation_id` is the row PK of `device_log_tails`; the dashboard polls by it; the audit row carries it; the agent echoes it on cmd-result. Same end-to-end discipline as the prior two slices.
- **ADR-012** (test policy): integration test for the new endpoint + the cmd-result router branch; agent unit tests for path-allow-list, binary refusal, line-cap, payload-cap.
- **ADR-013** (Phase 3 signed pipeline): `log.tail` ships as the **fifth unsigned dispatcher handler** alongside the four established in slice 2 + Phase 0. Per ADR-028's analysis: blast-radius is bounded (the agent only reads pre-allow-listed files; worst case an attacker who can publish to `devices/{id}/cmd` exfils a log file the operator could read anyway). The Phase 3 signed envelope wraps `log.tail` alongside the other four without rewriting handler logic. **An amend-by-narrow-scope of ADR-028 is appropriate** when this slice lands (note the fifth handler in the ADR; same risk bound).
- **ADR-018** (Fargate workers): no new container; `CmdResultIngester` gains a Type branch.
- **ADR-019** (Goose migrations): one migration `013_device_log_tails.sql`.
- **ADR-021** (CloudWatch): no new alarm. The cmd-result DLQ alarm — currently absent and tracked as a phase-2 followup — covers any handler-side regression here too.

## Open questions

- **Stale row cleanup.** Per-request rows accumulate. Either: (a) a sweeper goroutine in cp-ingest that deletes rows older than ~24h; (b) `pg_cron` job; (c) TTL via partition rotation. Going with (a) for slice scope — mirrors `internal/cp/ingest/sweeper.go`'s presence sweeper. Cap retention at 24h: long enough that a slow operator can re-poll a tab they left open overnight, short enough not to bloat.
- **Truncation semantics — head or tail when capped?** Operator asked for "last 500 lines" but file is huge. Agent reads from the tail backwards. If 500 lines × avg-line-length exceeds 200 KB, the agent should return the most-recent lines that fit and report `truncated: true` + `truncated_from_lines: 500` + `actual_lines: NNN`. Operator sees the freshest content; the dashboard surfaces the truncation.
- **What's in the per-OS allow-list?** Need to settle the canonical set before implementation. Talk to operations / read mac-mini-rollout's install modules. Tracked as a blocker in issue 01.

## Refinements expected mid-implementation

- **MQTT 256 KB hard limit verification.** AWS docs state "Maximum size of a payload for publish requests: 256 KiB". The 200 KB cap I've chosen carries 50 KB headroom for envelope + correlation_id + log_name + flags. Verify with a synthetic 200 KB log file once the slice is running; tighten if the broker rejects.
- **launchd-rotated logs.** macOS rotates `/var/log/install.log` to `.0`, `.1.gz`, etc. The slice handles the live file only; rotated files are out (operator can ask for a specific rotated file by name if it's in the allow-list, but we don't auto-merge across rotations).
- **Edge UI log path discovery.** The Edge UI is at `mac-mini-rollout/webui/`; need to confirm where it writes its logs at install time. Probably `/usr/local/uknomi/edge-ui.log` or similar. Check before settling the Mac allow-list.

## Success criteria for the PRD as a whole

- Operator can open a device's "Logs" tab, pick a file from the allow-list, click Fetch, and see the last N lines within ~3s (cmd round-trip + 2s poll).
- An operator who runs the slice repeatedly against the bench Mac sees no SQS DLQ growth, no agent-side crashes, no broken truncations.
- A path NOT in the agent's allow-list returns a clear error on the dashboard ("file not accessible"), audit log records the attempt.
- The success criterion of Phase 2 — "operators no longer SSH for read-only checks" — is fully met once this slice + slice 4 (Edge UI proxy) land. After this slice, the only remaining SSH-only use cases are interactive Edge UI access and live `tail -f`.
