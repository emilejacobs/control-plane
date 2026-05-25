# Issue 04 — Repo-root cleanup: `cp-ingest` binary + `slice2.tfplan`

Status: ready-for-agent
Type: AFK
Estimate: 5 min

## Parent

- Source: Slice 1 left a 19 MB `cp-ingest` Mach-O binary at the repo root from a manual build (already noted in [slice 2 issue 01](../../phase-2-allow-list-overrides/issues/01-end-to-end.md)). Slice 2 added `infra/terraform-deploy/slice2.tfplan` — a consumed terraform plan file from the live apply, no further value.

## What to build

Delete both, optionally add `.gitignore` entries to prevent recurrence.

### Concrete actions

```bash
rm cp-ingest infra/terraform-deploy/slice2.tfplan
```

If hand-building binaries at the repo root is a recurring habit, consider:

```gitignore
# Local-only built binaries from `go build ./cmd/...`
/cp-api
/cp-ingest
/cp-agent
/audit-mirror

# Consumed terraform plan files
*.tfplan
```

Pasted into the existing `.gitignore`.

## Acceptance criteria

- [ ] Both files gone from `git status` untracked list.
- [ ] If `.gitignore` was updated, the new entries cover the binary names that have been seen at the root historically.

## Why this matters

- `cp-ingest` at 19 MB clutters `git status`/`ls` output.
- `slice2.tfplan` is stale (terraform apply consumed it); a future user who tries `terraform apply slice2.tfplan` will hit the "Saved plan is stale" error and waste minutes diagnosing.
- Neither is dangerous; just hygiene.

## Blocked by

- None. Five-minute fix; pick up any time.
