"use client";

import { useParams } from "next/navigation";
import { useDevice, useNow } from "../../../lib/api/hooks";
import { UNASSIGNED } from "../../../lib/fleet";
import { PresenceChip } from "../../../components/PresenceChip";
import { CertExpiryIndicator } from "../../../components/CertExpiryIndicator";
import { formatAgo } from "../../../lib/ago";

// DevicePage is the per-device view: the static record, live presence, and
// the mTLS cert expiry for one device, polled every 10s (Issue 18).
export default function DevicePage() {
  const { id } = useParams<{ id: string }>();
  const device = useDevice(id);
  const now = useNow();
  const d = device.data;

  return (
    <main>
      {d && (
        <>
          <h1>{d.hostname}</h1>
          <PresenceChip online={d.isOnline} />
          <p>
            Last seen{" "}
            {d.lastSeenAt ? (
              <time>{formatAgo(d.lastSeenAt, now)}</time>
            ) : (
              "Never"
            )}
          </p>
          <CertExpiryIndicator
            expiresAt={d.certExpiresAt}
            daysRemaining={d.certDaysRemaining}
          />
          <dl>
            <dt>Client</dt>
            <dd>{d.clientName ?? UNASSIGNED}</dd>
            <dt>Site</dt>
            <dd>{d.siteName ?? UNASSIGNED}</dd>
            <dt>Hardware kind</dt>
            <dd>{d.hardwareKind}</dd>
            <dt>OS version</dt>
            <dd>{d.osVersion}</dd>
            <dt>Agent version</dt>
            <dd>{d.agentVersion}</dd>
            <dt>Hardware UUID</dt>
            <dd>{d.hardwareUuid}</dd>
            <dt>IoT Thing ARN</dt>
            <dd>{d.iotThingArn}</dd>
            <dt>Enrolled</dt>
            <dd>{d.enrolledAt.slice(0, 10)}</dd>
          </dl>
        </>
      )}
    </main>
  );
}
