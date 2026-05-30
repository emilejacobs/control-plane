# Architectural Decisions

Each decision is recorded as an ADR (Architecture Decision Record). Status values: **Accepted** | **Proposed** | **Superseded**. ADRs are immutable once accepted; reversals are recorded as new ADRs that supersede the prior one.

ADRs live as individual files under [`adr/`](./adr/). This file is the index; add one line per new ADR.

ADRs from ADR-009 onward follow the template at [`agents/adr-template.md`](./agents/adr-template.md), which adds a `Verification` section pointing at how each decision is enforced in code.

| #   | Title                                                                                            | Status                                |
| --- | ------------------------------------------------------------------------------------------------ | ------------------------------------- |
| 001 | [AWS IoT Core for command channel](./adr/0001-aws-iot-core-for-command-channel.md)               | Accepted (2026-05-05)                 |
| 002 | [Go agent, single cross-compiled binary](./adr/0002-go-agent-single-binary.md)                   | Accepted (2026-05-05)                 |
| 003 | [Hybrid command/data split — IoT Core + Tailscale](./adr/0003-hybrid-command-data-split.md)      | Accepted (2026-05-05)                 |
| 004 | [Install-script-driven enrollment, not MDM-driven](./adr/0004-install-script-enrollment.md)      | Accepted (2026-05-05)                 |
| 005 | [API-first design for mobile readiness](./adr/0005-api-first-for-mobile.md)                      | Accepted (2026-05-05)                 |
| 006 | [NextAuth dual-source auth, no Cognito](./adr/0006-nextauth-dual-source-auth.md)                 | Superseded by ADR-010 (2026-05-18)    |
| 007 | [Pi/Radxa minimal-agent only](./adr/0007-pi-radxa-minimal-agent.md)                              | Accepted (2026-05-05)                 |
| 008 | [Skip Zabbix integration](./adr/0008-skip-zabbix-integration.md)                                 | Accepted (2026-05-05)                 |
| 009 | [Go for the API service](./adr/0009-go-for-api-service.md)                                       | Accepted (2026-05-18)                 |
| 010 | [Local credentials only; drop Entra ID, NextAuth, Cognito](./adr/0010-local-credentials-only.md) | Accepted (2026-05-18)                 |
| 011 | [Structured JSON logs with end-to-end correlation IDs](./adr/0011-structured-logs-correlation-ids.md) | Accepted (2026-05-18)             |
| 012 | [Test policy — standard pyramid + idempotency + CI gate](./adr/0012-test-policy.md)              | Accepted (2026-05-18)                 |
| 013 | [Agent self-update in Phase 3 with auto-rollback; 1-year Phase 1 cert TTL](./adr/0013-agent-self-update-phase-3.md) | Accepted (2026-05-18) — amended by ADR-026 (cert TTL deferred to Phase 3) |
| 014 | [Bootstrap token distribution via S3](./adr/0014-bootstrap-token-via-s3.md)                      | Superseded by ADR-017 (2026-05-21)    |
| 015 | [Postgres multi-AZ from day one](./adr/0015-postgres-multi-az.md)                                | Accepted (2026-05-18) — amended by ADR-022 (Wave 0 single-AZ window) |
| 016 | [Telemetry retention — 30 days hot / 1 year cold](./adr/0016-telemetry-retention.md)             | Accepted (2026-05-18)                 |
| 017 | [Static bootstrap key bundled in install package](./adr/0017-static-bootstrap-key-in-install-package.md) | Accepted (2026-05-21)         |
| 018 | [Fargate workers (not Lambda) for all MQTT-side ingest](./adr/0018-fargate-workers-for-ingest.md) | Accepted (2026-05-21)                 |
| 019 | [Goose for schema migrations, embedded and run on startup](./adr/0019-goose-migrations-on-startup.md) | Accepted (2026-05-21)         |
| 020 | [CI/CD — trunk-based, prod + staging, manual promotion to prod](./adr/0020-ci-cd-trunk-based-staging-manual-promote.md) | Accepted (2026-05-21) — amended by ADR-027 (Phase 1 direct-to-prod auto-deploy) |
| 021 | [All-CloudWatch observability for Phase 1; OpenTelemetry deferred](./adr/0021-observability-all-cloudwatch.md) | Accepted (2026-05-21) |
| 022 | [Phase 1 deployment-infra shape](./adr/0022-phase-1-deployment-shape.md)                         | Accepted (2026-05-22) — amends ADR-015 |
| 023 | [Fargate scheduled tasks for periodic batch jobs](./adr/0023-fargate-scheduled-tasks-for-batch-jobs.md) | Accepted (2026-05-23)         |
| 024 | [Dashboard persists tokens in `localStorage` (Phase 1)](./adr/0024-dashboard-token-persistence-localstorage.md) | Accepted (2026-05-24)         |
| 025 | [Direct-to-main pushes under AFK-agent dev](./adr/0025-direct-to-main-pushes-under-afk-agent-dev.md) | Accepted (2026-05-24)         |
| 026 | [Phase 1 cert TTL deferral to Phase 3](./adr/0026-phase-1-cert-ttl-deferral.md) | Accepted (2026-05-24) — amends ADR-013 |
| 027 | [Phase 1 auto-deploy direct to prod; staging + promote gate deferred](./adr/0027-phase-1-auto-deploy-direct-to-prod.md) | Accepted (2026-05-24) — amends ADR-020 |
| 028 | [Unsigned `config.update` in Phase 2; signing arrives with the Phase 3 envelope](./adr/0028-unsigned-config-update-phase-2.md) | Accepted (2026-05-24) — narrows ADR-013 |
| 029 | [Edge UI rework — CP-authoritative, rewrite onto CP stack, drop unused features](./adr/0029-edge-ui-rework-scope.md) | Accepted (2026-05-25) |
| 030 | [Edge UI per-feature surface model](./adr/0030-edge-ui-per-feature-surface.md) | Accepted (2026-05-25) |
| 031 | [Webhook endpoint registry — CP-wide config primitive](./adr/0031-webhook-endpoint-registry.md) | Accepted (2026-05-25) |
| 032 | [Edge UI v1 — Next.js+Go, port 5051, plain HTTP, parallel install](./adr/0032-edge-ui-v1-stack-port-tls.md) | Accepted (2026-05-26) — clarifies ADR-030 §§ 1, 8 |
| 033 | [Clients/sites taxonomy — daily mirror sync from upstream HTTP API](./adr/0033-clients-sites-taxonomy-sync.md) | Accepted (2026-05-26) — supersedes the direct-MySQL plan in memory `external_mysql_taxonomy` |
| 034 | [Agent backend abstraction for an OS-agnostic command + probe surface](./adr/0034-agent-backend-abstraction-os-agnostic-surface.md) | Accepted (2026-05-28) |
| 035 | [Agent fleet-update — push+reconcile delivery, resident-wrapper rollback, derived rollout](./adr/0035-agent-fleet-update-mechanism.md) | Accepted (2026-05-29) — refines ADR-013; corrects "Ed25519 in KMS" |
