// TanStack Query hooks over the cp-api auth calls — the dashboard's only
// path to server state (Issue 16; no setInterval in components).
"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import {
  firstRun,
  getFirstRunStatus,
  login,
  enrollTotp,
  type LoginInput,
} from "./auth";
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

// useFirstRunStatus reports whether the system has been initialized. The
// root page mounts it to auto-redirect a visitor to the claim page when
// the operators table is empty. Cache long enough that login + every
// already-authenticated route doesn't re-fetch on every render.
export function useFirstRunStatus() {
  return useQuery({
    queryKey: ["first-run-status"],
    queryFn: getFirstRunStatus,
    staleTime: 60_000,
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

// useNow returns the current time, re-rendering every intervalMs. The clock
// runs through TanStack Query's refetchInterval — not a setInterval — so the
// per-device view's last_seen ago-string ticks every second between the 10s
// polls without breaking the structural no-setInterval rule.
export function useNow(intervalMs = 1_000): Date {
  const { data } = useQuery({
    queryKey: ["now", intervalMs],
    queryFn: () => Date.now(),
    refetchInterval: intervalMs,
    initialData: () => Date.now(),
  });
  return new Date(data);
}
