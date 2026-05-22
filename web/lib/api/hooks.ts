// TanStack Query hooks over the cp-api auth calls — the dashboard's only
// path to server state (Issue 16; no setInterval in components).
"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { firstRun, login, enrollTotp, type LoginInput } from "./auth";
import { getDevices, getDevice } from "./devices";

interface Credentials {
  email: string;
  password: string;
}

// useFirstRun claims the first-run admin account.
export function useFirstRun() {
  return useMutation({
    mutationFn: ({ email, password }: Credentials) => firstRun(email, password),
  });
}

// useLogin authenticates an operator; its result carries the
// requires_totp_enrollment flag the login page branches on.
export function useLogin() {
  return useMutation({
    mutationFn: (input: LoginInput) => login(input),
  });
}

// useEnrollTotp mints the TOTP secret + recovery codes for the operator.
export function useEnrollTotp() {
  return useMutation({
    mutationFn: () => enrollTotp(),
  });
}

// devicePollInterval is the fleet-view poll cadence (PRD: 10s polling, no
// WebSocket in Phase 1). Presence transitions surface within one cycle.
const devicePollInterval = 10_000;

// useDevices loads the operator's site-scoped fleet and re-polls every 10s,
// so the fleet view stays current without any setInterval in components.
export function useDevices() {
  return useQuery({
    queryKey: ["devices"],
    queryFn: getDevices,
    refetchInterval: devicePollInterval,
  });
}

// useDevice loads one device's full record and re-polls every 10s, so the
// per-device view's presence stays current on the same cadence as the fleet.
export function useDevice(id: string) {
  return useQuery({
    queryKey: ["device", id],
    queryFn: () => getDevice(id),
    refetchInterval: devicePollInterval,
  });
}
