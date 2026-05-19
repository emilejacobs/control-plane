# Issue tracker: Local Markdown

Issues and PRDs for this repo live as markdown files in `.scratch/`. There is no remote issue tracker (no GitHub remote yet — to be added later).

## Posture: ephemeral

Issues here are **short-lived working notes** — open design questions, "circle back to this," PRD drafts. They are not durable records.

- Durable design decisions belong in an **ADR**, not an issue. If a decision is worth surviving the eventual move to GitHub, write it up under `docs/adr/` instead.
- When a remote tracker is added, do **not** auto-migrate. Anything in `.scratch/` that still matters gets re-filed manually; anything stale gets discarded.

## Conventions

- One feature per directory: `.scratch/<feature-slug>/`
- The PRD is `.scratch/<feature-slug>/PRD.md`
- Implementation issues are `.scratch/<feature-slug>/issues/<NN>-<slug>.md`, numbered from `01`
- Triage state is recorded as a `Status:` line near the top of each issue file (see `triage-labels.md` for the role strings)
- Comments and conversation history append to the bottom of the file under a `## Comments` heading

## When a skill says "publish to the issue tracker"

Create a new file under `.scratch/<feature-slug>/` (creating the directory if needed).

## When a skill says "fetch the relevant ticket"

Read the file at the referenced path. The user will normally pass the path or the issue number directly.
