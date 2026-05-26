# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                                                    |
| -------------------------- | -------------------- | -------------------------------------------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue                                    |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information                                   |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent                                    |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation                                              |
| `wontfix`                  | `wontfix`            | Will not be actioned                                                       |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table via `gh issue create --label <label>` or `gh issue edit <n> --add-label <label>`. A newly-filed issue gets exactly one triage label.

## Done

"Done" is **closing the GitHub issue**. The canonical 5-role vocab expects issues to terminate by being closed in the tracker, not by carrying a `done` label.

Discharge the standing documentation criterion in a completion comment before closing — see [`issue-template.md`](./issue-template.md). Then `gh issue close <n> --comment "..."` (the comment is the completion summary).

For historical reference on closed work, GitHub's "Closed" filter on the issues list gives the same navigability the prior local-markdown `done` extension was reaching for.
