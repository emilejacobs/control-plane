# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Layout: single-context, under `docs/`

This repo is single-context. Domain documentation lives under `docs/` rather than at the repo root (following the existing repo convention of keeping all design docs in `docs/`):

- **`docs/CONTEXT.md`** — the glossary of domain terms (CP, Edge UI, Agent, Tailnet, Subnet router, Device shadow, …).
- **`docs/adr/`** — one file per Architecture Decision Record, numbered (`0001-*.md`, `0002-*.md`, …).
- **`docs/decisions.md`** — the ADR index. Lists every ADR with status and a link to its file. Add one line per new ADR.

Other design docs in `docs/` (`architecture.md`, `roadmap.md`, `costs.md`) are not domain glossaries or decisions and don't need to be read for every task — read them when their topic is relevant.

## Before exploring, read these

- **`docs/CONTEXT.md`** first — vocabulary anchors everything else.
- **`docs/adr/`** — read ADRs that touch the area you're about to work in. The index at `docs/decisions.md` is the fastest way to scan titles.

If any of these files don't exist for a given concept, **proceed silently**. Don't flag the absence; don't suggest creating them upfront. The producer skill (`/grill-with-docs`) creates them lazily when terms or decisions actually get resolved.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `docs/CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids — for example, use **Edge UI**, not "Talon" (which was the old name) or "local web UI".

If the concept you need isn't in the glossary yet, that's a signal — either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/grill-with-docs`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-005 (API-first design) — but worth reopening because…_

## Writing new ADRs

When creating ADR-009 or later, follow the template at [`adr-template.md`](./adr-template.md). New ADRs include a `**Verification.**` section so the enforcement of each decision (test path, lint rule, or `TBD`) is discoverable in code. The earlier ADRs (0001–0008) are frozen historical records and are not retroactively updated.
