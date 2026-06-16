// CP-singleton settings calls against cp-api (#84). The account-wide Plate
// Recognizer token is write-only over the API: setPRToken stores it;
// getPRTokenStatus reports only whether it is set — never the value.
import { apiRequest } from "./client";
import { ApiError } from "./auth";

export interface PRTokenStatus {
  isSet: boolean;
}

export async function getPRTokenStatus(): Promise<PRTokenStatus> {
  const res = await apiRequest("/settings/pr-token");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load PR token status");
  }
  const d = (await res.json()) as { is_set: boolean };
  return { isSet: d.is_set ?? false };
}

export async function setPRToken(token: string): Promise<void> {
  const res = await apiRequest("/settings/pr-token", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ token }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to update PR token");
  }
}
