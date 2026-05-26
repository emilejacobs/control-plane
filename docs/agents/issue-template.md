# Issue Template

The shape every implementation issue follows when filed on GitHub Issues. Tracker conventions are in [`issue-tracker.md`](./issue-tracker.md); this file is the body skeleton and the rationale for the standing documentation criterion.

## Format

The GitHub issue **title** is short, imperative, descriptive (no `Issue NN —` prefix; GitHub assigns the number). The **body** is markdown, in this shape:

```markdown
Type: <AFK | HITL>

## Parent

- PRD: [`.scratch/<feature>/PRD.md`](../../.scratch/<feature>/PRD.md) § <sections>
  (or the driving ADR: [`docs/adr/NNNN-...md`](../adr/NNNN-...md))
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

- #<issue-number>, or "None — can start immediately"
```

The triage label (one of `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix` — see [`triage-labels.md`](./triage-labels.md)) is applied as a GitHub label at create time, not as a body line.

Comments on the GitHub issue capture conversation as work progresses. The completion comment — posted just before `gh issue close` — is where the documentation criterion is discharged: either "docs updated: …" or an explicit "no documentation impact".

## The documentation criterion

Every issue carries the documentation acceptance criterion as a **standing item** — it is not feature-specific and not optional.

Under AFK-agent development with an architectural-reviewer-only human, the docs under `docs/` are not an end-of-project deliverable — they are *build inputs*. `CLAUDE.md` directs every agent to read `docs/CONTEXT.md` and `docs/architecture.md` before starting work. A stale architecture doc silently misdirects every later agent that reads it.

The cost is asymmetric. Updating a doc when the issue lands takes minutes — the implementing agent already has full context. Reconstructing it several issues later means re-reading the code to recover decisions that were obvious at the time. The criterion forces the cheap path, at the moment the work happens.

"No documentation impact" is a legitimate outcome — but it must be a *stated* decision in the completion comment, not a silent skip. That is what keeps the criterion honest: the reviewer can see the call was made deliberately.

Architecture diagrams live as Mermaid in `docs/architecture.md` so they diff in review and render in place — never as binary images.
