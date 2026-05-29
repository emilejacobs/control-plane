// Operator-management calls against cp-api (#16). Staff-only endpoints;
// non-staff callers get 403, which the /operators page catches to gate itself.
import { apiRequest } from "./client";
import { ApiError } from "./auth";

export interface Operator {
  id: string;
  email: string;
  isStaff: boolean;
  totpEnrolled: boolean;
  deactivated: boolean;
  siteIds: string[];
}

interface OperatorWire {
  id: string;
  email: string;
  is_staff: boolean;
  totp_enrolled: boolean;
  deactivated: boolean;
  site_ids: string[] | null;
}

function fromWire(o: OperatorWire): Operator {
  return {
    id: o.id,
    email: o.email,
    isStaff: o.is_staff,
    totpEnrolled: o.totp_enrolled,
    deactivated: o.deactivated,
    siteIds: o.site_ids ?? [],
  };
}

export async function getOperators(): Promise<Operator[]> {
  const res = await apiRequest("/operators");
  if (!res.ok) throw new ApiError(res.status, "failed to load operators");
  const body = (await res.json()) as { operators: OperatorWire[] };
  return (body.operators ?? []).map(fromWire);
}

export async function getOperator(id: string): Promise<Operator> {
  const res = await apiRequest(`/operators/${id}`);
  if (!res.ok) throw new ApiError(res.status, "failed to load operator");
  return fromWire((await res.json()) as OperatorWire);
}

export interface CreateOperatorInput {
  email: string;
  isStaff: boolean;
  siteIds: string[];
}

export interface CreateOperatorResult {
  operator: Operator;
  tempPassword: string;
}

export async function createOperator(input: CreateOperatorInput): Promise<CreateOperatorResult> {
  const res = await apiRequest("/operators", {
    method: "POST",
    body: JSON.stringify({ email: input.email, is_staff: input.isStaff, site_ids: input.siteIds }),
  });
  if (!res.ok) throw new ApiError(res.status, "failed to create operator");
  const body = (await res.json()) as { operator: OperatorWire; temp_password: string };
  return { operator: fromWire(body.operator), tempPassword: body.temp_password };
}

export interface UpdateOperatorInput {
  isStaff?: boolean;
  siteIds?: string[];
  resetTotp?: boolean;
  resetPassword?: boolean;
}

export interface UpdateOperatorResult {
  operator: Operator;
  // tempPassword is present only when resetPassword was requested.
  tempPassword?: string;
}

export async function updateOperator(id: string, input: UpdateOperatorInput): Promise<UpdateOperatorResult> {
  const res = await apiRequest(`/operators/${id}`, {
    method: "PUT",
    body: JSON.stringify({
      is_staff: input.isStaff,
      site_ids: input.siteIds,
      reset_totp: input.resetTotp ?? false,
      reset_password: input.resetPassword ?? false,
    }),
  });
  if (!res.ok) throw new ApiError(res.status, "failed to update operator");
  const body = (await res.json()) as { operator: OperatorWire; temp_password?: string };
  return { operator: fromWire(body.operator), tempPassword: body.temp_password };
}

export async function deactivateOperator(id: string): Promise<void> {
  const res = await apiRequest(`/operators/${id}/deactivate`, { method: "POST" });
  if (!res.ok) throw new ApiError(res.status, "failed to deactivate operator");
}

export async function reactivateOperator(id: string): Promise<void> {
  const res = await apiRequest(`/operators/${id}/reactivate`, { method: "POST" });
  if (!res.ok) throw new ApiError(res.status, "failed to reactivate operator");
}
