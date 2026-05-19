# ADR-016: Telemetry retention — 30 days hot in Timestream, 1 year cold via S3

**Status:** Accepted (2026-05-18)

**Context.** Heartbeat and per-service metrics flow into Timestream. Hot retention in Timestream is convenient and queryable but more expensive than S3. Long-term retention is valuable for trend analysis and incident postmortems but rarely accessed.

**Decision.**

- Timestream **hot retention: 30 days**. Sufficient for dashboard live views and recent-incident debugging.
- Daily export of Timestream data to S3 (parquet, partitioned by date). **Cold retention: 1 year.**
- Query path for cold data: Athena over S3. Not user-facing; available for analyst or agent queries when needed.
- Data older than 1 year is discarded.

**Consequences.**
- (+) Hot path stays cheap and fast.
- (+) Year-long history available for trend analysis and postmortem.
- (+) Athena query cost is pay-per-query; idle cost ~$0.
- (-) Export pipeline (daily Lambda or scheduled Fargate task) is one more thing to operate.
- (-) Data older than 1 year is gone. Acceptable — this is operational telemetry, not business data.

**Verification.** TBD — added at implementation. Integration test covers: the export pipeline produces well-formed parquet partitioned by date; an Athena query against an exported dataset returns expected rows.
