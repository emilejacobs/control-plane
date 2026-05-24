# ADR-025: Direct-to-main pushes under AFK-agent dev; branch protection deliberately bypassed

**Status:** Accepted (2026-05-24)

**Context.** The project's dev model (see the auto-memory `dev_model.md`) is AFK-agent development: AI agents write nearly all code, with a single human acting as architectural reviewer. ADRs 009–023 were made under this constraint and rely on it — ADR-020 (CI/CD trunk-based, manual promote to prod) and ADR-012 (test policy as the CI gate) in particular assume that the trunk moves fast and the safety net is the test suite, not pre-merge human review.

GitHub has rulesets enabled on `main` (require PR, 3 required status checks), but the repo owner has admin bypass. Every push by the agent during Phase 1 deployment used the bypass; the operator-UX round did the same. The handoff after that round flagged this as a follow-on: "Decide deliberately whether to tighten (require PRs for non-trivial changes) or formally relax (AFK-agent dev model)."

Three real options:

1. **Keep current arrangement: rules exist, admin bypass is normal.** The agent pushes directly; the rules are a tripwire if a non-admin push is ever attempted. Test/build CI still runs after the push (post-hoc). This matches what's actually happening today.
2. **Formally relax: delete the rulesets.** Same behaviour, but no "Bypassed rule violations" noise on every push. Loses the tripwire.
3. **Tighten: require PR + passing checks pre-merge, no bypass.** Every change goes through a PR. Slower trunk velocity. The agent would need PR-create + merge permissions; with no second human reviewer, the agent would self-approve trivial PRs, which adds ceremony without adding a check.

Option 3 trades visible PR review for ceremony that no human will actually exercise — the architectural-reviewer human reviews ADRs and discussion, not every line diff. Option 2 loses the audit signal that something unusual happened. Option 1 keeps the AFK-agent velocity while preserving a paper trail.

**Decision.** Keep the current arrangement: GitHub rulesets on `main` stay enabled (require PR, 3 status checks), but admin bypass remains available and is the normal path for the AFK agent. The agent pushes directly to `main`. The bypass surfaces in the CLI output (`remote: Bypassed rule violations for refs/heads/main`) and in GitHub's audit log, so any push that wasn't intended is still observable.

Authorization to push remains scoped to the architectural-reviewer human's account; the agent runs as that account in `git push` invocations and inherits its admin bypass.

**Consequences.**
- (+) Matches the dev model. Trunk moves at agent speed; the architectural-reviewer human is not blocked on micro-reviews.
- (+) Bypass events remain visible in the GitHub audit log, so anomalous pushes can be spotted post-hoc.
- (+) No change to ADR-020 (trunk-based, manual promote to prod) — that ADR remains the operative CI/CD posture.
- (-) Post-hoc CI is the only enforcement. A test that depends on infrastructure outside the test suite (e.g., the `web/package-lock.json` portability gotcha noted in the prior handoff) can land broken and only be caught when CI runs.
- (-) When a second human joins the team and starts contributing PRs, this ADR needs to be revisited — non-admin contributors will hit the rulesets without bypass, which is the desired behaviour for them but means this ADR no longer describes the whole policy.

**Verification.** N/A — environmental/policy decision. The rulesets themselves are configured in the GitHub repository settings, not in this repo. The agent's push transcripts show the `Bypassed rule violations` notice on every direct push, which is the running verification that the arrangement is in effect.
