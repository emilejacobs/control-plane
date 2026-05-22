// Auth API calls against cp-api: first-run admin claim, login, TOTP
// enrollment. Each stores the returned token pair on success.
import { apiRequest, setTokens } from "./client";

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

export interface LoginResult {
  // requiresTotpEnrollment is true for an operator who has not yet enrolled
  // TOTP — the dashboard routes them into enrollment before anything else.
  requiresTotpEnrollment: boolean;
}

// login authenticates an operator. A TOTP code or, in its place, a recovery
// code may accompany the password. On success the token pair is stored.
export async function login(input: LoginInput): Promise<LoginResult> {
  const res = await apiRequest("/auth/login", {
    method: "POST",
    body: JSON.stringify({
      email: input.email,
      password: input.password,
      totp_code: input.totpCode ?? "",
      recovery_code: input.recoveryCode ?? "",
    }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "login failed");
  }
  const body = (await res.json()) as TokenPair & { requires_totp_enrollment: boolean };
  setTokens({ accessToken: body.access_token, refreshToken: body.refresh_token });
  return { requiresTotpEnrollment: body.requires_totp_enrollment };
}
