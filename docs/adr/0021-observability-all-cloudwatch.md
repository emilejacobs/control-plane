# ADR-021: All-CloudWatch observability for Phase 1; OpenTelemetry deferred

**Status:** Accepted (2026-05-21)

**Context.** ADR-011 settled structured JSON logs with end-to-end correlation IDs. The rest of the observability stack — metrics, traces, alarms, dashboards — was left open. Phase 1 needs a settled choice before Issue 21 (alarms) can ship.

Three realistic shapes were considered:

1. **All-CloudWatch SDK direct.** Code uses AWS SDK calls (`cloudwatch:PutMetricData`, `aws-xray-sdk-go`) to emit metrics and traces. CloudWatch Alarms over CloudWatch Metrics. CloudWatch Dashboards for visualization. Roughly $0 incremental cost over the AWS baseline.
2. **OpenTelemetry SDK in code, exporting via an OTel Collector sidecar to CloudWatch + X-Ray.** Future backend portability (swap CloudWatch for Datadog/Grafana later by changing collector config, not code). Costs ~$15-20/month at Phase 1 sizing (two services × two environments = four sidecars at ~$4/month each).
3. **OpenTelemetry SDK exporting *directly* to CloudWatch (no collector).** Preserves OTel API in code at near-zero cost; less standard pattern, narrower community support; a future collector swap is a config change rather than a refactor.

Rejected as out of scope for Phase 1:

- **Datadog / Grafana Cloud / self-hosted Prometheus.** Datadog's pricing is punishing at scale. Grafana Cloud's free tier is generous but introduces a second console, second auth surface, second billing relationship. Self-hosted Prometheus runs the wrong direction for a team minimizing ops burden.

The substantive trade-off between options 1 and 2: $15-20/month for *optionality* — the ability to swap observability backends later without re-instrumenting code. At Phase 1's fleet size and cost discipline, that optionality does not pay for itself. The migration to OTel later (if and when CloudWatch becomes painful) is a cost paid only if the pain materializes; today's $15-20/month is real money saved with high confidence.

Option 3 is a defensible middle path but trades community-standard practice for marginal future savings. Phase 1 picks the simpler endpoint.

**Decision.** Phase 1 uses **all-CloudWatch SDK direct** for metrics, traces, alarms, and dashboards.

Concrete shape:

- **Metrics:** `cp-api` and `cp-ingest` emit custom metrics via the AWS SDK's `cloudwatch.PutMetricData` (batched up to 1000 per call). A thin wrapper package `internal/cp/metrics` exposes `Counter`, `Gauge`, `Histogram` helpers so call sites are clean and the SDK is replaceable later. Custom metrics namespace: `CP/<service>` (e.g., `CP/Ingest/sweeper_lag_seconds`).
- **Traces:** AWS X-Ray SDK (`aws-xray-sdk-go`). Auto-instrumented for the HTTP server, SQS consumer, and `pgx` driver where wrappers exist. Custom segments for enrollment, login, sweeper tick. X-Ray's 100k traces/month free tier covers Phase 1 with margin.
- **Alarms:** CloudWatch Alarms over CloudWatch Metrics, defined in Terraform. The Phase 1 alarm set lives in Issue 21. Paging via SNS → email-to-Slack for Phase 1 (paid pager service revisited if fleet grows).
- **Dashboards:** CloudWatch Dashboards defined in Terraform. One dashboard per service plus a fleet-health overview.
- **Logs:** unchanged from ADR-011 — `slog` → stdout → CloudWatch Logs via the Fargate log driver.

**Consequences.**

- (+) Zero incremental cost beyond the baseline CloudWatch usage that exists regardless of metric source.
- (+) One console, one auth surface, one billing line, one place to look for everything observability-related.
- (+) Standard, well-documented patterns for AWS-native shops. Future engineers (or AI agents) recognize the SDK calls immediately without OTel context.
- (+) Alarms and dashboards in Terraform mean the observability config is reviewable in PRs alongside the code it observes.
- (-) **No backend portability.** Switching to Datadog/Grafana/etc. later requires re-instrumenting every metric call site. We accept this — the swap is a Phase 5+ concern, the migration cost is uncertain, and the $15-20/month avoidance is concrete now.
- (-) X-Ray's UI and querying are weaker than Tempo/Jaeger. Acceptable at Phase 1's modest trace volume; revisit if tracing becomes a regular debugging surface.
- (-) The `cloudwatch.PutMetricData` API is uglier than OTel's `meter.Counter` style. Mitigated by the `internal/cp/metrics` wrapper.

**Verification.** TBD — added at implementation. Integration tests cover:

- `tests/integration/metrics_test.go::TestHeartbeatIncrementsCounter` — recording a heartbeat in the ingest worker results in a `CP/Ingest/heartbeats_processed` metric data point being submitted (verified against a fake CloudWatch client).
- `tests/integration/metrics_test.go::TestPutMetricDataIsBatched` — emitting 10 metrics in a tight loop results in 1 (not 10) `PutMetricData` call when the batcher flushes.
- The alarm set itself is verified by Issue 21's acceptance criteria (each alarm triggered once).
