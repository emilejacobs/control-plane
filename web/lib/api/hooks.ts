// TanStack Query hooks over the cp-api auth calls — the dashboard's only
// path to server state (Issue 16; no setInterval in components).
"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  firstRun,
  getFirstRunStatus,
  login,
  enrollTotp,
  setPassword,
  type LoginInput,
} from "./auth";
import {
  getOperators,
  createOperator,
  updateOperator,
  deactivateOperator,
  reactivateOperator,
  type CreateOperatorInput,
  type UpdateOperatorInput,
} from "./operators";
import { getDevices, getDevice, getCameras, getHealthProbes, getNetworkScan, setSnapshotCadence, setALPRLicense, commissionDevice, getPRConfig, putPRConfig, type PRConfigInput } from "./devices";
import type { SnapshotCadence } from "./devices";
import {
  getPRTokenStatus,
  setPRToken,
  getNotificationSettings,
  setNotificationConfig,
  setTeamsWebhook,
} from "./settings";
import { getDeviceCaptures, getCaptureUrl, requestSnapshot } from "./captures";
import { getFleetAlerts, getFleetCameras } from "./fleet";
import {
  getAgentRollout,
  getAgentVersions,
  startRollout,
  abortRollout,
  type StartRolloutInput,
} from "./rollouts";
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

// useSetPassword completes the set-new-password flow (#16) for an operator on
// a system-generated temp password.
export function useSetPassword() {
  return useMutation({
    mutationFn: (newPassword: string) => setPassword(newPassword),
  });
}

// useOperators loads the staff-only operator list (#16). No polling — the
// list only changes on explicit admin actions, which invalidate this query.
export function useOperators() {
  return useQuery({
    queryKey: ["operators"],
    queryFn: getOperators,
  });
}

// useCreateOperator / useUpdateOperator / useSetOperatorActive are the
// /operators page mutations; each invalidates the list on success.
export function useCreateOperator() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateOperatorInput) => createOperator(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["operators"] }),
  });
}

export function useUpdateOperator(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: UpdateOperatorInput) => updateOperator(id, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["operators"] }),
  });
}

export function useSetOperatorActive(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (active: boolean) => (active ? reactivateOperator(id) : deactivateOperator(id)),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["operators"] }),
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

// useFleetCameras loads the site-scoped fleet camera roll-up (#152) for the
// Overview Cameras gauge + Camera alerts panel, on the same 10s cadence as the
// rest of the dashboard.
export function useFleetCameras() {
  return useQuery({
    queryKey: ["fleet-cameras"],
    queryFn: getFleetCameras,
    refetchInterval: devicePollInterval,
  });
}

// useAgentRollout loads the site-scoped fleet-update rollout view (#42) —
// roll-up counts + per-device desired-vs-reported state — on the same 10s
// cadence as the fleet list, so convergence ticks forward without a refresh.
export function useAgentRollout() {
  return useQuery({
    queryKey: ["agent-rollout"],
    queryFn: getAgentRollout,
    refetchInterval: devicePollInterval,
  });
}

// useAgentVersions loads the published catalog versions (#42) that back the
// rollout target picker. Long staleTime — the catalog only changes when a new
// release is cut, not on dashboard interactions.
export function useAgentVersions() {
  return useQuery({
    queryKey: ["agent-versions"],
    queryFn: getAgentVersions,
    staleTime: 5 * 60_000,
  });
}

// useStartRollout is the start/abort/promote mutation (POST /agent-rollouts,
// staff-only). On success it invalidates the rollout view so the table reflects
// the newly-stamped desired versions on the next render.
export function useStartRollout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: StartRolloutInput) => startRollout(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agent-rollout"] }),
  });
}

// useAbortRollout reverts un-converged devices to their reported version
// (#42 Slice C). On success it invalidates the rollout view.
export function useAbortRollout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (devices: { id: string; reportedVersion: string }[]) => abortRollout(devices),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agent-rollout"] }),
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

// useSetSnapshotCadence updates a device's scheduled-snapshot cadence (#9). On
// success it invalidates the device record so the picker reflects the saved value.
export function useSetSnapshotCadence(deviceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (cadence: SnapshotCadence) => setSnapshotCadence(deviceId, cadence),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["device", deviceId] }),
  });
}

// useSetALPRLicense stores a device's Plate Recognizer license (#84). On
// success it invalidates the device record so the set/not-set badge updates.
export function useSetALPRLicense(deviceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (license: string) => setALPRLicense(deviceId, license),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["device", deviceId] }),
  });
}

// Plate Recognizer per-device config (#5). The query feeds the edit form; the
// mutation PUTs (which also pushes pr.config.update to the device) and
// invalidates so the "pending → applied" state refreshes.
export function useDevicePRConfig(deviceId: string) {
  return useQuery({
    queryKey: ["pr-config", deviceId],
    queryFn: () => getPRConfig(deviceId),
  });
}

export function usePutPRConfig(deviceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: PRConfigInput) => putPRConfig(deviceId, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["pr-config", deviceId] }),
  });
}

// useCommissionDevice triggers Commission on a device (#91). On success it
// invalidates the device record so the page reflects the result.
export function useCommissionDevice(deviceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => commissionDevice(deviceId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["device", deviceId] }),
  });
}

// usePRTokenStatus reads whether the account-wide PR token is set (#84). The
// raw token is never returned.
export function usePRTokenStatus() {
  return useQuery({
    queryKey: ["pr-token-status"],
    queryFn: getPRTokenStatus,
  });
}

// useSetPRToken stores the account-wide PR token (#84). On success it
// invalidates the status query so the card reflects "set".
export function useSetPRToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (token: string) => setPRToken(token),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["pr-token-status"] }),
  });
}

// useNotificationSettings reads the notification config — enable switch,
// recipient list, and whether the Teams webhook is set (#96). The webhook
// value is never returned.
export function useNotificationSettings() {
  return useQuery({
    queryKey: ["notification-settings"],
    queryFn: getNotificationSettings,
  });
}

// useSetNotificationConfig writes the non-secret config (enabled + recipients).
// On success it invalidates the settings query so the card reflects the save.
export function useSetNotificationConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (cfg: { enabled: boolean; emailRecipients: string[] }) =>
      setNotificationConfig(cfg.enabled, cfg.emailRecipients),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["notification-settings"] }),
  });
}

// useSetTeamsWebhook stores the write-only Teams webhook URL (#96). On success
// it invalidates the settings query so the card reflects "configured".
export function useSetTeamsWebhook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (webhookUrl: string) => setTeamsWebhook(webhookUrl),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["notification-settings"] }),
  });
}

// useDeviceSnapshots loads a device's snapshot captures newest-first (#8). Same
// 10s device-page cadence, so a freshly-captured snapshot surfaces within a poll.
export function useDeviceSnapshots(deviceId: string) {
  return useQuery({
    queryKey: ["device", deviceId, "captures", "snapshot"],
    queryFn: () => getDeviceCaptures(deviceId, "snapshot"),
    refetchInterval: devicePollInterval,
  });
}

// useCaptureUrl resolves a short-lived signed download URL for one capture (#8).
// staleTime is just under the server's ~5-min TTL so a thumbnail isn't re-signed
// on every render but never serves an expired URL.
export function useCaptureUrl(captureId: string | null) {
  return useQuery({
    queryKey: ["capture-url", captureId],
    queryFn: () => getCaptureUrl(captureId as string),
    enabled: captureId !== null,
    staleTime: 4 * 60_000,
  });
}

// useRequestSnapshot triggers an on-demand snapshot for a camera (#8). On
// success it invalidates the device's snapshot list so the new row (and its
// thumbnail) appears as soon as the agent's ACK lands.
export function useRequestSnapshot(deviceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (cameraId: string) => requestSnapshot(deviceId, cameraId),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["device", deviceId, "captures", "snapshot"] }),
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
