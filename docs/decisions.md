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
| 013 | [Agent self-update in Phase 3 with auto-rollback; 1-year Phase 1 cert TTL](./adr/0013-agent-self-update-phase-3.md) | Accepted (2026-05-18) |
| 014 | [Bootstrap token distribution via S3](./adr/0014-bootstrap-token-via-s3.md)                      | Superseded by ADR-017 (2026-05-21)    |
| 015 | [Postgres multi-AZ from day one](./adr/0015-postgres-multi-az.md)                                | Accepted (2026-05-18)                 |
| 016 | [Telemetry retention — 30 days hot / 1 year cold](./adr/0016-telemetry-retention.md)             | Accepted (2026-05-18)                 |
| 017 | [Static bootstrap key bundled in install package](./adr/0017-static-bootstrap-key-in-install-package.md) | Accepted (2026-05-21)         |
| 018 | [Fargate workers (not Lambda) for all MQTT-side ingest](./adr/0018-fargate-workers-for-ingest.md) | Accepted (2026-05-21)                 |
| 019 | [Goose for schema migrations, embedded and run on startup](./adr/0019-goose-migrations-on-startup.md) | Accepted (2026-05-21)         |
| 020 | [CI/CD — trunk-based, prod + staging, manual promotion to prod](./adr/0020-ci-cd-trunk-based-staging-manual-promote.md) | Accepted (2026-05-21) |
| 021 | [All-CloudWatch observability for Phase 1; OpenTelemetry deferred](./adr/0021-observability-all-cloudwatch.md) | Accepted (2026-05-21) |
