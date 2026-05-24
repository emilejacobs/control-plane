# Issue 04 — DB connectivity from operator laptop (decision + implementation)

Status: needs-info
Type: HITL (decision), then AFK (implementation)
Estimate: 30 min decision; implementation depends on the path

## Parent

- Wave 0 handoff follow-on #7.
- Recurring friction: ad-hoc `psql` / SQL work for the CP (e.g. inspecting `devices` rows around an enrollment incident; the orphan-GC tool in [issue 01](./01-orphan-gc-tool.md) wants a fast local-iteration story too).
- ADR-022 § 1 ("two Terraform roots, single state bucket") and § 3 ("single NAT, accept brief AZ-degraded egress") set the surrounding posture.

## The forcing function

Tailscale's subnet route is approved but the RDS security group does not allow 5432 from the Tailscale router's SG. Three options surfaced at handoff time:

1. **SG rule on `aws_security_group.rds`** allowing 5432 from `aws_security_group.tailscale`. Trivial Terraform change (~5 lines). Lowest cost; gives every Tailnet operator direct `psql` against prod. Downsides: weakest blast-radius story (operator typo → prod data); no audit trail beyond Postgres logs.
2. **One-off ECS task with `psql`** — operator runs `aws ecs run-task` with an image containing `psql`, gets an interactive session via `aws ecs execute-command`. Better auditability (run-task events in CloudTrail). Same effective access; the ergonomic bar is higher than `psql --host` and might end up unused.
3. **Custom Go tool** (e.g. `cmd/cp-sql/`) — a scoped query interface that the operator runs as an ECS one-off, exposing only the queries we want operators issuing (read-only common queries; explicit `--write` flag for the orphan-GC case). Best blast-radius; most build cost; highest ongoing maintenance.

## The decision needed

Pick one of (1)/(2)/(3) before implementing. The right answer depends on:

- How often operators actually need DB access (today: rarely; one or two incidents per quarter).
- Whether the orphan-GC tool [#01](./01-orphan-gc-tool.md) gets its own ECS-task wrapper anyway (if yes, (3) is mostly built already; (1) becomes redundant). 
- Operator-team size trajectory (single architectural-reviewer human today per `dev_model` memory; if the team grows, blast-radius matters more).

## Suggested first step

Write a short ADR (or extend this issue with the decision rationale, then promote to ADR) capturing which path was picked and why. Then the implementation is downstream.

## Acceptance criteria

- [ ] Decision recorded — either in a new ADR or as a comment block at the top of this issue.
- [ ] If (1) is chosen: Terraform diff on `infra/terraform-deploy/security-groups.tf` allowing 5432 from `aws_security_group.tailscale`, with a CIDR or SG-source reference (not `0.0.0.0/0`). Plan + apply. Verified via `psql --host` from an operator laptop on the Tailnet.
- [ ] If (2) or (3) is chosen: the relevant binary/task-def exists and is documented in `docs/runbooks/db-access.md`.

## Blocked by

- The decision itself. Implementation is fast once the path is picked.
