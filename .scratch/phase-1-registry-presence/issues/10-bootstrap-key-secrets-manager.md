# Issue 10 — Bootstrap key in Secrets Manager + CI integration + production hardening

Status: ready-for-agent
Type: AFK

## Parent

- PRD: [`PRD.md`](../PRD.md) § User Stories 1, 6–7, § Implementation Decisions (ADR-017).
- ADR: ADR-017 (static bootstrap key bundled in install package; Secrets Manager → CI → package).

## What to build

The production-grade bootstrap-key plumbing: store the static key in AWS Secrets Manager, fetch it from `mac-mini-rollout`'s CI at install-package build time, validate it server-side at enrollment, and ship the production hardening (rate limit, hostname-convention anomaly alert) that ADR-017 specified. Replaces the env-var-based dev shortcut from #03.

Scope:

- Terraform: `uknomi/cp/bootstrap-key` secret in Secrets Manager. IAM role for `mac-mini-rollout` CI scoped to read that one secret. Rotation policy documented (~6 month cadence, manual today).
- `mac-mini-rollout` CI pipeline (in the sister repo): on every install-package build, the CI workflow assumes the IAM role, fetches the secret, and bakes it into the install package. The bootstrap key never lands in the rollout repo's git history.
- The CP API service loads the key from Secrets Manager at startup (not env var), with refresh-on-401 if rotation happens mid-deploy.
- Enrollment endpoint hardening per ADR-017: per-source-IP rate limit (20 req/hour) at the ALB or in middleware; hostname-convention regex check (regex pinned in code, currently `^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$`) emits an audit-log alert on mismatch but does not block enrollment; audit log records source IP + hardware UUID + hostname + outcome for every request.
- A page-threshold rule (more than 10 enrollments from a single source IP in 10 minutes) wired in #21.

## Acceptance criteria

- [ ] A `terraform apply` provisions the secret and the IAM role.
- [ ] `mac-mini-rollout` CI fetches the secret and produces an install package with the key embedded; the rollout repo's git history does not contain the secret.
- [ ] CP API service loads the key from Secrets Manager and validates it on `POST /enrollments`; the env-var path from #03 is removed.
- [ ] A 21st enrollment request in an hour from a single source IP returns 429.
- [ ] An enrollment request with a hostname that doesn't match the convention regex still completes (201) but writes an audit alert.
- [ ] Integration test confirms: unknown key → 401; rate limit trips at threshold; anomaly alert fires.

## Blocked by

- Issue 02 (CI/CD pipeline shape).
- Issue 03 (enrollment endpoint exists in dev-mode form).
