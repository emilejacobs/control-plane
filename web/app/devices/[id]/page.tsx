"use client";

import type React from "react";
import { useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { useQueryClient } from "@tanstack/react-query";
import {
  useDevice,
  useNow,
  useCameras,
  useHealthProbes,
  useNetworkScan,
  useSitesTree,
  useUpdateDeviceDeployment,
} from "../../../lib/api/hooks";
import {
  postCamera,
  putCamera,
  deleteCamera,
  postNetworkScan,
  edgePreviewURL,
  edgeLanURL,
  type Camera,
} from "../../../lib/api/devices";
import { UNASSIGNED } from "../../../lib/fleet";
import { PresenceChip } from "../../../components/PresenceChip";
import { CertExpiryIndicator } from "../../../components/CertExpiryIndicator";
import { ServicesPanel } from "../../../components/ServicesPanel";
import { HealthPanel } from "../../../components/HealthPanel";
import { CamerasPanel } from "../../../components/CamerasPanel";
import { SnapshotCadenceControl } from "../../../components/SnapshotCadenceControl";
import { ALPRLicenseControl } from "../../../components/ALPRLicenseControl";
import { CommissionButton } from "../../../components/CommissionButton";
import { CameraDialog } from "../../../components/CameraDialog";
import { NetworkScanModal } from "../../../components/NetworkScanModal";
import { EditServicesModal } from "../../../components/EditServicesModal";
import { EditDeploymentModal } from "../../../components/EditDeploymentModal";
import { LogsPanel } from "../../../components/LogsPanel";
import { Topbar } from "../../../components/ui/Topbar";
import { Card } from "../../../components/ui/Card";
import { KV } from "../../../components/ui/KV";
import { Pill } from "../../../components/ui/Pill";
import { RequireAuth } from "../../../components/RequireAuth";
import { formatAgo } from "../../../lib/ago";

// DevicePage is the per-device view: static record + live presence + the
// mTLS cert expiry, polled every 10s (Issue 18).
//
// The design exposes panels (Services, live CPU/Memory, Cameras, Tailscale
// IP, etc.) that GET /devices/{id} does not yet return. Per the bundle
// integration decision, those panels do not render — the page surfaces
// only what the API actually provides.
//
// Test contract preserved: <h1>{hostname}, KV pairs by <dt> label, the
// CertExpiryIndicator's "Certificate expires …" text, the "N seconds ago"
// ago-string ticking between polls.
export default function DevicePage() {
  const { id } = useParams<{ id: string }>();
  const device = useDevice(id);
  const cameras = useCameras(id);
  const healthProbes = useHealthProbes(id);
  const now = useNow();
  const d = device.data;
  const camData = cameras.data;
  const queryClient = useQueryClient();
  const [editingServices, setEditingServices] = useState(false);
  const [editingDeployment, setEditingDeployment] = useState(false);
  const sitesTree = useSitesTree();
  const updateDeployment = useUpdateDeviceDeployment(id);
  // Dialog state for the cameras CRUD UI. null = closed.
  // mode === "add" → empty form; "edit"/"delete" carry the target row.
  // The optional `prefillIp` on "add" is set by the NetworkScanModal's
  // "Add as camera" button so the operator's flow flows: scan → click →
  // dialog opens with the candidate IP already in the RTSP URL field.
  const [cameraDialog, setCameraDialog] = useState<
    | { mode: "add"; prefillIp?: string }
    | { mode: "edit"; camera: Camera }
    | { mode: "delete"; camera: Camera }
    | null
  >(null);
  // Network scan correlation_id we're currently polling, or null if no
  // scan is in flight. The modal opens when this is non-null.
  const [scanCorrelationId, setScanCorrelationId] = useState<string | null>(
    null,
  );
  // True while the POST /network-scan is in flight (before a correlation_id
  // comes back) — distinct from the poll phase below. scanError surfaces a
  // failed POST inline instead of failing silently (#12).
  const [scanPosting, setScanPosting] = useState(false);
  const [scanError, setScanError] = useState<string | null>(null);
  const networkScan = useNetworkScan(id, scanCorrelationId);
  // The scan is "in flight" from the click until the poll reports done/error:
  // the POST round-trip, the pre-first-poll gap, and every pending poll. Drives
  // the button's disabled/"Scanning…" affordance (#12).
  const scanInFlight =
    scanPosting ||
    (scanCorrelationId !== null &&
      networkScan.data?.status !== "done" &&
      networkScan.data?.status !== "error");

  async function handleCameraSubmit(input: {
    label: string;
    rtspUrl: string;
    isLpr: boolean;
  }) {
    if (cameraDialog?.mode === "add") {
      await postCamera(id, input);
    } else if (cameraDialog?.mode === "edit") {
      await putCamera(id, cameraDialog.camera.cameraId, input);
    } else if (cameraDialog?.mode === "delete") {
      await deleteCamera(id, cameraDialog.camera.cameraId);
    }
    setCameraDialog(null);
    void queryClient.invalidateQueries({ queryKey: ["device", id, "cameras"] });
  }

  // handleScanNetwork kicks off (or re-runs) a LAN scan. An empty cidr means
  // auto-detect; a non-empty one overrides the subnet (#12). The POST is
  // wrapped so a failure surfaces inline rather than silently swallowing the
  // click.
  async function handleScanNetwork(cidr?: string) {
    setScanError(null);
    setScanPosting(true);
    try {
      const { correlationId } = await postNetworkScan(id, cidr ? { cidr } : {});
      setScanCorrelationId(correlationId);
    } catch (e) {
      setScanError(
        e instanceof Error ? e.message : "Failed to start network scan",
      );
    } finally {
      setScanPosting(false);
    }
  }

  // Cert "pill" tone derived from days remaining — mirrors the band logic
  // in CertExpiryIndicator without duplicating its text shape.
  const certTone = d?.certDaysRemaining == null
    ? "neutral"
    : d.certDaysRemaining < 0
      ? "red"
      : d.certDaysRemaining <= 30
        ? "amber"
        : "green";
  const certLabel =
    d?.certDaysRemaining == null
      ? "Unknown"
      : d.certDaysRemaining < 0
        ? "Expired"
        : `${d.certDaysRemaining}d left`;

  return (
    <RequireAuth>
      <Topbar />
      <main className="page">
        {device.isPending && (
          <div role="status" className="muted" style={{ padding: 12 }}>
            Loading device…
          </div>
        )}

        {device.isError && (
          <Card>
            <div role="alert" className="row" style={{ gap: 12 }}>
              <p style={{ margin: 0 }}>Could not load device.</p>
              <button className="btn" onClick={() => device.refetch()}>
                Refresh
              </button>
            </div>
          </Card>
        )}

        {d && (
          <>
            <div className="page-header">
              <div>
                <div className="crumbs">
                  <Link href="/devices">Fleet</Link>
                  <span className="sep">/</span>
                  {d.clientName ? (
                    <span>{d.clientName}</span>
                  ) : (
                    <span className="muted">{UNASSIGNED}</span>
                  )}
                  {d.siteName && (
                    <>
                      <span className="sep">/</span>
                      <span className="muted">{d.siteName}</span>
                    </>
                  )}
                </div>
                <h1 className="page-title mono" style={{ fontSize: 24 }}>
                  {d.hostname}
                </h1>
                <div className="row" style={{ gap: 12, marginTop: 8 }}>
                  <PresenceChip online={d.isOnline} />
                  <span className="muted" style={{ fontSize: 13 }}>
                    Last seen{" "}
                    {d.lastSeenAt ? (
                      <time>{formatAgo(d.lastSeenAt, now)}</time>
                    ) : (
                      "Never"
                    )}
                  </span>
                  <span style={{ width: 1, height: 16, background: "var(--line)" }} />
                  <Pill tone={certTone}>Cert · {certLabel}</Pill>
                </div>
              </div>
            </div>

            <div className="detail-grid" style={{ marginBottom: 16 }}>
              <Card label="System">
                <KV
                  items={[
                    ["Hostname", <span className="mono">{d.hostname}</span>],
                    ["Hardware kind", d.hardwareKind],
                    ["OS version", d.osVersion],
                    ["LAN IP", networkField(d.lanIp)],
                    ["Tailscale IP", networkField(d.tailscaleIp)],
                    ["Tailscale name", networkField(d.tailscaleName)],
                  ]}
                />
              </Card>

              <Card
                label="Deployment"
                actions={
                  <button
                    type="button"
                    onClick={() => setEditingDeployment(true)}
                    className="btn"
                  >
                    Edit
                  </button>
                }
              >
                <KV
                  items={[
                    ["Client", d.clientName ?? UNASSIGNED],
                    ["Site", d.siteName ?? UNASSIGNED],
                    ["Asset number", d.assetNumber ?? UNASSIGNED],
                    ["Agent version", <span className="mono">{d.agentVersion}</span>],
                    [
                      "Hardware UUID",
                      <span className="mono" style={{ fontSize: 11.5 }}>
                        {d.hardwareUuid}
                      </span>,
                    ],
                    ["Enrolled", d.enrolledAt.slice(0, 10)],
                    [
                      "IoT Thing ARN",
                      <span className="mono" style={{ fontSize: 11, color: "var(--ink-2)" }}>
                        {d.iotThingArn}
                      </span>,
                    ],
                  ]}
                />
                <div style={{ marginTop: 14, paddingTop: 14, borderTop: "1px solid var(--line, #eee)" }}>
                  <CommissionButton deviceId={id} assigned={d.siteId != null} />
                </div>
              </Card>
            </div>

            <Card label="mTLS Certificate">
              <div className="row" style={{ gap: 24, alignItems: "flex-start", flexWrap: "wrap" }}>
                <div style={{ flex: 1, minWidth: 280 }}>
                  <div className="row" style={{ gap: 10 }}>
                    <div
                      className="stat-value"
                      style={{ fontSize: 26 }}
                    >
                      {d.certDaysRemaining == null
                        ? "Unknown"
                        : d.certDaysRemaining < 0
                          ? "Expired"
                          : `${d.certDaysRemaining} days`}
                    </div>
                    <Pill tone={certTone}>{certLabel}</Pill>
                  </div>
                  <div className="muted" style={{ marginTop: 4, fontSize: 12.5 }}>
                    <CertExpiryIndicator
                      expiresAt={d.certExpiresAt}
                      daysRemaining={d.certDaysRemaining}
                    />
                  </div>
                  {d.certDaysRemaining != null && (
                    <div className="cert-bar">
                      <div
                        className={`fill ${
                          d.certDaysRemaining < 0
                            ? "crit"
                            : d.certDaysRemaining <= 7
                              ? "crit"
                              : d.certDaysRemaining <= 30
                                ? "warn"
                                : ""
                        }`}
                        style={{
                          width: `${Math.max(0, Math.min(100, (d.certDaysRemaining / 365) * 100))}%`,
                        }}
                      />
                    </div>
                  )}
                </div>
              </div>
            </Card>

            <div style={{ height: 16 }} />

            <Card
              label={
                <span>
                  Services{" "}
                  <span className="muted" style={{ fontSize: 11, fontWeight: 400 }}>
                    {d.serviceConfig.allowListOverride !== null
                      ? "(overridden)"
                      : "(default)"}
                  </span>
                </span>
              }
              actions={
                <button
                  type="button"
                  onClick={() => setEditingServices(true)}
                  style={{
                    padding: "4px 10px",
                    fontSize: 12,
                    background: "transparent",
                    border: "1px solid var(--line, #ccc)",
                    borderRadius: 4,
                    cursor: "pointer",
                  }}
                >
                  Edit
                </button>
              }
            >
              <ServicesPanel services={d.services} now={now} />
            </Card>

            <div style={{ height: 16 }} />

            <Card label="Health">
              <HealthPanel probes={healthProbes.data ?? []} now={now} />
            </Card>
            {editingServices && d && (
              <EditServicesModal
                device={d}
                onClose={() => setEditingServices(false)}
                onApplied={() => {
                  void queryClient.invalidateQueries({ queryKey: ["device", id] });
                }}
              />
            )}
            {editingDeployment && d && (
              <EditDeploymentModal
                tree={sitesTree.data ?? []}
                currentSiteId={d.siteId}
                currentAssetNumber={d.assetNumber ?? null}
                saving={updateDeployment.isPending}
                onSubmit={async (input) => {
                  await updateDeployment.mutateAsync(input);
                  setEditingDeployment(false);
                }}
                onClose={() => setEditingDeployment(false)}
              />
            )}

            <div style={{ height: 16 }} />

            <Card label="Cameras">
              <div
                style={{
                  display: "flex",
                  justifyContent: "flex-end",
                  flexWrap: "wrap",
                  gap: 16,
                  marginBottom: 10,
                }}
              >
                <ALPRLicenseControl deviceId={id} licenseSet={d.alprLicenseSet} />
                <SnapshotCadenceControl deviceId={id} cadence={d.snapshotCadence} />
              </div>
              <CamerasPanel
                cameras={camData?.cameras ?? []}
                lastAppliedAt={camData?.lastAppliedAt ?? null}
                deviceId={id}
                now={now}
                onAddCamera={() => setCameraDialog({ mode: "add" })}
                onEditCamera={(c) => setCameraDialog({ mode: "edit", camera: c })}
                onDeleteCamera={(c) => setCameraDialog({ mode: "delete", camera: c })}
                onScanNetwork={() => handleScanNetwork()}
                scanInFlight={scanInFlight}
                scanError={scanError}
                onVerifyAngle={(c) =>
                  window.open(edgePreviewURL(d, c.cameraId), "_blank", "noopener")
                }
                previewURL={(c) => edgePreviewURL(d, c.cameraId)}
                // Issue #14: render the LAN-URL affordance only
                // when the device's lan_ip telemetry has landed.
                // Pre-rollout devices (lanIp = null) keep the
                // single-button shape.
                lanURL={
                  d.lanIp
                    ? (c) => edgeLanURL(d, c.cameraId) as string
                    : undefined
                }
              />
            </Card>
            {cameraDialog && (
              <CameraDialog
                mode={cameraDialog.mode}
                camera={"camera" in cameraDialog ? cameraDialog.camera : undefined}
                prefillIp={"prefillIp" in cameraDialog ? cameraDialog.prefillIp : undefined}
                onSubmit={handleCameraSubmit}
                onClose={() => setCameraDialog(null)}
              />
            )}
            {scanCorrelationId && (
              <NetworkScanModal
                scan={networkScan.data ?? null}
                onClose={() => setScanCorrelationId(null)}
                onAddCamera={(ip) => {
                  setScanCorrelationId(null);
                  setCameraDialog({ mode: "add", prefillIp: ip });
                }}
                onRescan={(cidr) => handleScanNetwork(cidr)}
              />
            )}

            <div style={{ height: 16 }} />

            <Card label="Logs">
              <LogsPanel deviceId={id} />
            </Card>
          </>
        )}
      </main>
    </RequireAuth>
  );
}

// networkField renders a System-card row value for one of the
// heartbeat-published network identifiers (lanIp / tailscaleIp /
// tailscaleName, issue #14). Pre-rollout devices and devices whose
// detectors returned empty (e.g. no Tailscale binary on PATH) leave
// the field null; surface that as muted "Unknown" rather than blank.
function networkField(value: string | null): React.ReactNode {
  if (!value) {
    return <span className="muted">Unknown</span>;
  }
  return <span className="mono">{value}</span>;
}
