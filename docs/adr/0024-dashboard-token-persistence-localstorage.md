# ADR-024: Dashboard persists tokens in `localStorage` (Phase 1)

**Status:** Accepted (2026-05-24)

**Context.** ADR-010 settled on local credentials hosted in the Go API: short-lived JWT access tokens (1h) and longer-lived refresh tokens (24h) stored server-side, issued and rotated by `cp-api`. The dashboard is a Next.js app at `https://control.uknomi.com` calling `https://api.control.uknomi.com` — same eTLD+1, separate subdomain.

The initial dashboard kept the operator's tokens in a module-level variable in `web/lib/api/client.ts`. That works for SPA navigation but every full-page reload (Cmd-R, opening a deep link in a new tab, browser restart) discards the tokens and forces the operator back through password + TOTP. The handoff after the operator-UX round documented this as a near-term follow-on; Wave 0 bench operators will hit it constantly.

Real options, with the relevant trade-offs:

1. **In-memory only (status quo).** Safe against persistent XSS exfil because nothing survives a reload, but the operator gets logged out on every reload — unworkable.
2. **`sessionStorage`.** Survives within one tab, but clears on tab close. Doesn't survive Cmd-R-then-restore-tab or "open in new tab" — same UX failure mode as in-memory, just a smaller blast radius.
3. **`localStorage`.** Survives reloads and restarts. Token pair is reachable from any script in the page (XSS exfil risk). Tokens are short-lived (1h access, 24h refresh), the dashboard has no third-party script tags, and CSP can be tightened in a later round to narrow the exfil surface.
4. **`httpOnly` cookies on `api.control.uknomi.com`, set by `cp-api`.** Not reachable from JS, so persistent-XSS exfil is blocked. Forces a CSRF defence on every mutating route (the bearer-token model is implicitly CSRF-safe because the token is not auto-attached by the browser). Cross-subdomain cookie + CORS credentials adds two coupled knobs; the mobile client (ADR-005) cannot use a browser cookie and would still need a bearer flow. Bigger build, bigger blast radius if misconfigured.

Phase 1 is a 25-operator internal tool managed by a tiny team behind Tailscale. The dominant risk is operator UX, not a persistent-XSS-from-an-untrusted-script-tag attack — the dashboard has no third-party scripts and CSP is restrictive. The cookie route's CSRF and cross-subdomain complexity is real cost paid for protection against an attack the dashboard does not yet have a vector for.

**Decision.** The dashboard persists the operator's access + refresh token pair in `localStorage` under the key `uknomi.tokens` as a JSON `{accessToken, refreshToken}` object. `client.ts` reads the key at module load, mirrors writes on `setTokens` / `clearTokens`, and silently ignores parse failures (treated as no token). Token lifetimes stay at the ADR-010 values (1h access / 24h refresh).

This is explicitly a Phase 1 choice. Phase 2 will revisit it together with CSP hardening; if the dashboard grows third-party script surface or starts handling materially more sensitive data, the move is to `httpOnly` refresh cookie + in-memory access token, accepting the CSRF + cross-subdomain cost.

**Consequences.**
- (+) Operators stay signed in across reload, deep-link, and browser restart. The Sign out button (and an expired refresh token) are the only ways to lose the session.
- (+) Zero backend change. CORS already allows `Authorization` from the dashboard origin; no cookie attributes to coordinate.
- (+) Mobile (ADR-005) keeps the same bearer-token flow — no per-client divergence.
- (-) A successful XSS on the dashboard can exfil both tokens. Mitigations available without changing this ADR: tight CSP (no third-party scripts), no `dangerouslySetInnerHTML`, and the 1h access-token TTL already caps the steady-state window.
- (-) A stolen token pair stays usable until the refresh expires (24h) or the operator signs out. ADR-024 pairs with the follow-on "server-side `POST /auth/logout`" change so Sign out actually revokes the refresh token row.

**Verification.**
- `web/lib/api/client.test.ts` — round-trip test that `setTokens` then a fresh module load reads the pair back; `clearTokens` empties `localStorage`.
- The auth-flow MSW tests assert that a logged-in dashboard reload still attaches the bearer token (covers the persistence path end-to-end at the client layer).
