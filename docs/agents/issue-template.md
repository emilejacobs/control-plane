# Issue Template

The shape every implementation issue under `.scratch/<feature>/issues/` follows. File-naming, the `Status:` line, and the ephemeral posture are covered in [`issue-tracker.md`](./issue-tracker.md); this file is the skeleton and the rationale for the standing documentation criterion.

## Format

```markdown
# Issue NN — Title

Status: <triage role — see triage-labels.md>
Type: <AFK | HITL>

## Parent

- PRD: [`PRD.md`](../PRD.md) § <relevant sections>
- ADRs: <the ADRs this work must honour>

## What to build

Prose describing the slice, then a `Scope:` list of what is in — and an
explicit note of what is deliberately deferred to a later issue.

## Acceptance criteria

- [ ] <observable, testable outcome>
- [ ] <observable, testable outcome>
- [ ] **Documentation updated.** `docs/architecture.md` reflects any
      module, component, key flow, or cloud-infra change; `docs/CONTEXT.md`
      reflects any new or changed domain term; a hard-to-reverse decision is
      captured as an ADR. If the issue touches none of these, say so
      explicitly in the completion comment.

## Blocked by

- <issues or external work that must land first, or "None">
```

`## Comments` is not part of the initial issue — it is appended at the bottom on completion (see [`issue-tracker.md`](./issue-tracker.md)). The completion comment is where the documentation criterion is discharged: either "docs updated: …" or an explicit "no documentation impact".

## The documentation criterion

Every issue carries the documentation acceptance criterion as a **standing item** — it is not feature-specific and not optional.

Under AFK-agent development with an architectural-reviewer-only human, the docs under `docs/` are not an end-of-project deliverable — they are *build inputs*. `CLAUDE.md` directs every agent to read `docs/CONTEXT.md` and `docs/architecture.md` before starting work. A stale architecture doc silently misdirects every later agent that reads it.

The cost is asymmetric. Updating a doc when the issue lands takes minutes — the implementing agent already has full context. Reconstructing it several issues later means re-reading the code to recover decisions that were obvious at the time. The criterion forces the cheap path, at the moment the work happens.

"No documentation impact" is a legitimate outcome — but it must be a *stated* decision in the completion comment, not a silent skip. That is what keeps the criterion honest: the reviewer can see the call was made deliberately.

Architecture diagrams live as Mermaid in `docs/architecture.md` so they diff in review and render in place — never as binary images.
