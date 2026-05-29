// TanStack Query hooks over the cp-api auth calls — the dashboard's only
// path to server state (Issue 16; no setInterval in components).
"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  firstRun,
  getFirstRunStatus,
  login,
  enrollTotp,
  type LoginInput,
} from "./auth";
import { getDevices, getDevice, getCameras, getHealthProbes, getNetworkScan } from "./devices";
import { getFleetAlerts } from "./fleet";
import {
  getSitesTree,
  updateDeviceDeployment,
  type DeploymentUpdate,
} from "./taxonomy";

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

// useFleetAlerts loads the site-scoped fleet alerts roll-up (#21) for the
// Overview dashboard, on the same 10s cadence as the fleet list it joins
// against for drill-down hostnames.
export function useFleetAlerts() {
  return useQuery({
    queryKey: ["fleet-alerts"],
    queryFn: getFleetAlerts,
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

// useCameras loads the per-device cameras inventory (Phase 2 Edge UI
// rework, issue #2). Same 10s poll cadence as the rest of the device
// page so the "pending vs applied" badge flips fresh after an ACK.
export function useCameras(id: string) {
  return useQuery({
    queryKey: ["device", id, "cameras"],
    queryFn: () => getCameras(id),
    refetchInterval: devicePollInterval,
  });
}

// useHealthProbes loads the per-device fleet-health-probe snapshot
// (Phase 2, issue #19). Same 10s poll cadence as the rest of the device
// page; probe reports land every 5 minutes so the panel is never more
// than a poll behind the latest report.
export function useHealthProbes(id: string) {
  return useQuery({
    queryKey: ["device", id, "health-probes"],
    queryFn: () => getHealthProbes(id),
    refetchInterval: devicePollInterval,
  });
}

// useNetworkScan polls one specific scan request (Phase 2 Edge UI
// rework, issue #3). 2s cadence while pending — fast enough that the
// modal feels live, slow enough that the typical ~10s scan doesn't
// drown the API in polls. Refetch stops once status flips to done or
// error (the polled side is immutable after that).
const networkScanPollInterval = 2_000;
export function useNetworkScan(deviceId: string, correlationId: string | null) {
  return useQuery({
    queryKey: ["device", deviceId, "network-scan", correlationId],
    queryFn: () => getNetworkScan(deviceId, correlationId as string),
    enabled: correlationId !== null,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (data?.status === "done" || data?.status === "error") return false;
      return networkScanPollInterval;
    },
  });
}

// useSitesTree loads the clients/sites picker tree. Long staleTime
// because the upstream daily sync is the only realistic mutator;
// dashboard interactions don't change this shape. Force-sync from the
// Settings page invalidates this query when the dashboard reloads.
export function useSitesTree() {
  return useQuery({
    queryKey: ["sites-tree"],
    queryFn: getSitesTree,
    staleTime: 5 * 60_000,
  });
}

// useUpdateDeviceDeployment is the EditDeploymentModal's save mutation.
// On success it invalidates the per-device record so the Deployment
// card re-renders with the new site_name / client_name / asset_number.
export function useUpdateDeviceDeployment(deviceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: DeploymentUpdate) =>
      updateDeviceDeployment(deviceId, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["device", deviceId] });
      qc.invalidateQueries({ queryKey: ["devices"] });
    },
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
