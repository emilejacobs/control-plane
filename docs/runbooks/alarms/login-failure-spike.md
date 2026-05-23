# Alarm — `uknomi-cp-login-failure-spike`

**Fires when**: more than 100 `audit.login` lines with `outcome=failure` land in the cp-api log group over a 5-minute window.

**Why it matters**: ADR-017 hardens the login surface with per-account lockout and the bcrypt cost; a brute-force attempt against a known operator may not trip the lockout (the attacker spreads across many accounts) but will spike this metric. The alarm pages before the account-level lockout signals would, when the lockouts are still distributed across operators.

## What to check first

1. **Are the failures concentrated on one email or spread?** This is the single most useful triage signal.
   ```bash
   aws logs start-query --log-group-name /uknomi-cp/cp-api \
     --start-time $(date -u -v-15M +%s) --end-time $(date -u +%s) \
     --query-string 'fields @timestamp, email, source_ip, reason
                     | filter msg = "audit.login" and outcome = "failure"
                     | stats count(*) by email, source_ip
                     | sort by count(*) desc
                     | limit 20'
   ```
   - **One email + few IPs** → likely a forgotten password or a misconfigured client; reach out to that operator before locking the account.
   - **Many emails + one IP** → credential-stuffing from a single source; coordinate with the operator-account holders and block the IP at the WAF or ALB.
   - **Many emails + many IPs** → distributed; consider tightening the rate limit on `/auth/login` (currently account-level only; ALB-level rate limit is the Phase 2 lever).

2. **Check the `reason` distribution.**
   `invalid_credentials` dominates → password guessing. `invalid_totp` dominates → TOTP brute force (rare; the TOTP rate of guessing meaningful codes is low). `account_locked` dominates → the per-account lockout is already absorbing the load; the metric is noisy but not actionable.

3. **Recent operator activity.** If a fleet rollout is in progress and operators are running install scripts, the cp-agent enrollment flow uses `/enrollments` not `/auth/login` — so this alarm is not the rollout's noise. If you see it during a rollout, it's a real signal.

## Escalation

- If the pattern matches credential-stuffing (many emails / one IP): block the source IP via `aws_lb_listener_rule` precedence or the next-step WAF (file a follow-up if WAF isn't yet deployed).
- If a single operator account is being targeted: invalidate any active refresh tokens for that operator (`DELETE FROM refresh_tokens WHERE operator_id = $1` against the deploy DB; the next cycle of the operator's session will force a fresh login + TOTP).
- If volume sustains past 30 minutes, page the security on-call (currently the same person — file a one-line note in the audit log via the dashboard once it exists).
