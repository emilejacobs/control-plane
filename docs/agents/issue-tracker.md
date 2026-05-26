# Issue tracker: GitHub Issues

Implementation work for this repo is tracked on **GitHub Issues** at https://github.com/emilejacobs/control-plane/issues. Skills that say "publish to the issue tracker" or "fetch the relevant ticket" mean GitHub.

## What lives where

| Artifact | Where | Why |
|---|---|---|
| **PRDs / design docs** | `.scratch/<feature-slug>/PRD.md` (in-tree) | Often longer than fits a GitHub issue body; benefits from being diff'd in PRs alongside code; lives near the ADRs it references. |
| **Implementation issues** | GitHub Issues | Standard tracker; integrates with PRs; agent-discoverable via `gh`. |
| **Architectural decisions** | `docs/adr/` | Durable, indexed at `docs/decisions.md`. **ADRs are never issues** — they survive feature lifecycles. |
| **Working notes / open questions** | `.scratch/<feature-slug>/` | Ephemeral. Anything that turns out to be durable gets promoted to an ADR. |

`.scratch/` has historical content from before GitHub Issues was wired up (closed Phase-0/1 work, several Phase-2 slices). Leave that as-is for reference — no migration. New work uses the split above.

## Conventions for GitHub issues

- **Title**: short, imperative, descriptive (e.g. "Cameras inventory in CP + agent sync"). No issue-number prefix in the title — GitHub assigns the number.
- **Body**: follows [`issue-template.md`](./issue-template.md) — parent reference, scope, acceptance criteria with the standing documentation criterion, blockers.
- **Triage**: applied as a GitHub label. The five-role vocabulary is in [`triage-labels.md`](./triage-labels.md). A newly-filed issue gets exactly one triage label.
- **Blocked-by**: written in the body's "Blocked by" section as `#<issue-number>` references. GitHub renders them as cross-links automatically.
- **Done**: close the issue. A completion comment on the closed issue captures any "documentation criterion discharged: …" notes (per the issue template).

## Multi-slice feature work

When a feature breaks into multiple implementation slices (typically via the `/to-issues` skill):

1. The PRD or driving ADR lives in-tree (`.scratch/<feature>/PRD.md` or `docs/adr/NNNN-...md`).
2. Each slice = one GitHub issue. The body's "Parent" section links to the in-tree PRD/ADR via a markdown link to its path (GitHub renders these as clickable links to the file at the issue's commit).
3. Slice issues reference each other via `#NN` blockers.
4. An optional **epic** issue can be filed to group the slices; not required.

## When a skill says "publish to the issue tracker"

Run `gh issue create --title "<title>" --body "<body>" --label "<triage-label>"`. The body should match [`issue-template.md`](./issue-template.md). Capture the returned URL for any callers that need it.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number>` (or take a URL/number from the user). Issue bodies + comments give the full state.
