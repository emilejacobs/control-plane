// Auth API calls against cp-api: first-run admin claim, login, TOTP
// enrollment. Each stores the returned token pair on success.
import { apiRequest, currentTokens, setTokens, clearTokens } from "./client";

// ApiError carries cp-api's HTTP status so callers can branch on it.
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

interface TokenPair {
  access_token: string;
  refresh_token: string;
}

export interface FirstRunStatus {
  initialized: boolean;
}

// getFirstRunStatus polls the unauthenticated GET /auth/first-run so the
// root page can decide whether to route a visitor to the claim flow vs.
// rendering the overview. Cheap (one DB count) and safe pre-auth.
export async function getFirstRunStatus(): Promise<FirstRunStatus> {
  const res = await apiRequest("/auth/first-run", { method: "GET" });
  if (!res.ok) {
    throw new ApiError(res.status, "first-run status failed");
  }
  return (await res.json()) as FirstRunStatus;
}

// firstRun claims the first-run admin account. On success the returned token
// pair is stored, leaving the dashboard authenticated as the new admin.
export async function firstRun(email: string, password: string): Promise<void> {
  const res = await apiRequest("/auth/first-run", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "first-run failed");
  }
  const body = (await res.json()) as TokenPair;
  setTokens({ accessToken: body.access_token, refreshToken: body.refresh_token });
}

export interface LoginInput {
  email: string;
  password: string;
  totpCode?: string;
  recoveryCode?: string;
}

// LoginOutcome is the result of a login attempt for the two-step UI.
//  - "authenticated": password (and, for enrolled operators, the 2FA code)
//    accepted; tokens are stored. The flags drive onboarding routing.
//  - "totpRequired": the password verified but a valid TOTP code is still
//    needed — the UI shows (or stays on) the 2FA step. New operators (no TOTP
//    yet) never hit this: they authenticate on password alone.
export type LoginOutcome =
  | { kind: "authenticated"; requiresTotpEnrollment: boolean; mustChangePassword: boolean }
  | { kind: "totpRequired" };

// login authenticates an operator. Step one sends email + password (no code);
// for an enrolled operator that returns "totpRequired", and a second call adds
// the TOTP (or recovery) code. On full success the token pair is stored. A
// bad email/password throws ApiError(401) with no Reason header.
export async function login(input: LoginInput): Promise<LoginOutcome> {
  // A login starts a fresh session: drop any stale tokens so apiRequest's
  // 401-refresh path can't fire on the login call (it would otherwise try to
  // rotate a dead token instead of surfacing the 401 + Reason header).
  clearTokens();
  const res = await apiRequest("/auth/login", {
    method: "POST",
    body: JSON.stringify({
      email: input.email,
      password: input.password,
      totp_code: input.totpCode ?? "",
      recovery_code: input.recoveryCode ?? "",
    }),
  });
  // Password OK, second factor missing/invalid → cp-api flags it so we can
  // advance to the 2FA step instead of reporting bad credentials.
  if (res.status === 401 && res.headers.get("Reason") === "totp-required") {
    return { kind: "totpRequired" };
  }
  if (!res.ok) {
    throw new ApiError(res.status, "login failed");
  }
  const body = (await res.json()) as TokenPair & {
    requires_totp_enrollment: boolean;
    must_change_password?: boolean;
  };
  setTokens({ accessToken: body.access_token, refreshToken: body.refresh_token });
  return {
    kind: "authenticated",
    requiresTotpEnrollment: body.requires_totp_enrollment,
    mustChangePassword: body.must_change_password ?? false,
  };
}

// setPassword completes the constrained set-new-password flow (#16) for an
// operator on a system-generated temp password. Reachable while must-change
// is armed; the server reads the operator id from the bearer token.
export async function setPassword(newPassword: string): Promise<void> {
  const res = await apiRequest("/auth/password", {
    method: "POST",
    body: JSON.stringify({ new_password: newPassword }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to set password");
  }
}

// logout asks cp-api to revoke the operator's refresh token so a stolen
// pair cannot rotate forward after Sign out. It is fire-and-forget — the
// access token in memory is the source of truth for the local session, so
// callers (Topbar) clear tokens + navigate regardless of network outcome.
// A best-effort revoke is materially safer than a no-op, but a failed
// revoke must not strand the operator on the dashboard.
export async function logout(): Promise<void> {
  const tokens = currentTokens();
  if (tokens === null) return;
  try {
    await apiRequest("/auth/logout", {
      method: "POST",
      body: JSON.stringify({ refresh_token: tokens.refreshToken }),
    });
  } catch {
    // Network errors are intentionally swallowed; see comment above.
  }
}

export interface TotpEnrollment {
  provisioningUri: string;
  recoveryCodes: string[];
}

// enrollTotp mints the operator's TOTP secret and recovery codes. The
// provisioning URI is rendered as a QR code; the recovery codes are shown
// exactly once. Requires an authenticated session.
export async function enrollTotp(): Promise<TotpEnrollment> {
  const res = await apiRequest("/auth/totp/enroll", { method: "POST" });
  if (!res.ok) {
    throw new ApiError(res.status, "totp enrollment failed");
  }
  const body = (await res.json()) as {
    provisioning_uri: string;
    recovery_codes: string[];
  };
  return {
    provisioningUri: body.provisioning_uri,
    recoveryCodes: body.recovery_codes,
  };
}
