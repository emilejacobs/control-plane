# Issue 10 — Bootstrap key in Secrets Manager + CI integration + production hardening

Status: done
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

- [x] A `terraform apply` provisions the secret and the IAM role. *(HCL written and `terraform validate`-clean; `apply` is a deploy step — not run here, no AWS creds.)*
- [ ] `mac-mini-rollout` CI fetches the secret and produces an install package with the key embedded; the rollout repo's git history does not contain the secret. *(Deferred to the `mac-mini-rollout` repo — different codebase; see completion comment.)*
- [x] CP API service loads the key from Secrets Manager and validates it on `POST /enrollments`; the env-var path from #03 is removed.
- [x] A 21st enrollment request in an hour from a single source IP returns 429.
- [x] An enrollment request with a hostname that doesn't match the convention regex still completes (201) but writes an audit alert.
- [x] Integration test confirms: unknown key → 401; rate limit trips at threshold; anomaly alert fires.
- [x] **Documentation updated.** `docs/architecture.md` reflects any module, component, key flow, or cloud-infra change; `docs/CONTEXT.md` reflects any new or changed domain term; a hard-to-reverse decision is captured as an ADR. If the issue touches none of these, say so explicitly in the completion comment.

## Blocked by

- Issue 02 (CI/CD pipeline shape).
- Issue 03 (enrollment endpoint exists in dev-mode form).

## Comments

### 2026-05-22 — CP-side landed in 12 cycles (`9e02095`..`<docs>`); AC2 deferred

The production bootstrap-key plumbing and ADR-017's enrollment hardening.

- Cycles 1–3: enrollment auditing — success, failure (reason
  `invalid_bootstrap_key`), and the `audit.enrollment.anomaly` alert for a
  hostname off the `^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$` convention
  (still enrols — sanity check, not an allowlist).
- Cycles 4–5: per-source-IP rate-limit middleware — fixed window,
  21st request in the hour → 429. (Window-reset + IP-isolation guards.)
- Cycles 6–8: `internal/cp/bootstrap` — `Verifier` (cached key,
  constant-time compare, fail-fast eager load), refresh-on-mismatch so a
  key rotated mid-deploy is honoured without a restart, and the Secrets
  Manager-backed `KeyLoader`.
- Cycle 9: `registry.Config` takes a `BootstrapVerifier` interface;
  cp-api builds it from Secrets Manager at startup — `CP_BOOTSTRAP_KEY`
  env var removed.
- Cycle 10: rate limiter wired onto `POST /enrollments`; AC6 integration
  tests (`tests/integration/bootstrap_key_test.go`).
- Cycle 11: Terraform — `uknomi/cp/bootstrap-key` secret + scoped CI IAM
  role (`terraform validate`-clean).
- Cycle 12: docs.

**Scope decisions (confirmed with the user up front).**

- *Rate limiter* — in-memory per-process middleware. With N Fargate tasks
  the effective limit is 20·N; fine for Phase 1 wave volumes, and #21's
  page-threshold rule is the real abuse backstop.
- *AC2 (the `mac-mini-rollout` install-package CI)* — **deferred**. It
  lives in the sister repo, is not part of this codebase, and is not
  TDD-able from here. It should be filed on the `mac-mini-rollout`
  tracker: the CI workflow assumes `aws_iam_role.bootstrap_ci` (output
  `bootstrap_ci_role_arn`), reads `uknomi/cp/bootstrap-key`, and bakes the
  value into the install package without committing it.

**Deploy steps not run here (no AWS creds).** `terraform apply`; then
`aws secretsmanager put-secret-value` to replace the placeholder with the
real key. The Terraform seeds a non-secret placeholder so the secret is
usable on first apply; `ignore_changes` keeps Terraform off the real
value thereafter.

**Documentation criterion.** Discharged — `architecture.md` § Enrollment,
the module table (`bootstrap` added; `registry`/`api` updated), and the
cloud-infra "Current state" list now describe the Secrets Manager flow,
the rate limit, and the audit/anomaly behavior. ADR-017's Verification
section points at the real tests. `CONTEXT.md` unchanged — "bootstrap
key" is already glossed and ADR-017 already owns the decision; #10 only
implements it.
