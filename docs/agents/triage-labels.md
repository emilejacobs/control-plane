# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                                                    |
| -------------------------- | -------------------- | -------------------------------------------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue                                    |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information                                   |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent                                    |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation                                              |
| `wontfix`                  | `wontfix`            | Will not be actioned                                                       |
| *(local extension)*        | `done`               | Acceptance criteria met; issue file kept for reference (see note below)    |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

## About the `done` extension

`done` is **local to this repo**, not part of the canonical Matt Pocock 5-role vocabulary. The canonical vocab expects issues to terminate by being closed in the remote tracker (GitHub Issues, etc.). Since this repo uses local-markdown (no remote tracker yet), we extend the vocab with `done` so completed issue files can be kept for reference rather than deleted.

- Move an issue to `done` once its acceptance criteria are fully met.
- The triage skill won't transition to `done` — apply it manually after a PR or batch of cycles lands.
- When GitHub is added later, `done` issues either get re-filed as closed GH issues (rare; only if useful as reference) or stay as local markdown for historical context.

Since the tracker is local markdown (see `issue-tracker.md`), labels are written as a `Status: <label>` line near the top of each issue file.

## Note on the current phase

This repo is in **design phase** — no code yet. The `ready-for-agent` label is effectively dormant until code lands (an AFK agent can't usefully advance a design question). The vocab is kept canonical so it activates naturally once implementation work begins; no relabel needed later.
