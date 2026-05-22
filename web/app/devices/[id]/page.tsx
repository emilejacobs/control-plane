"use client";

import { useParams } from "next/navigation";
import { useDevice } from "../../../lib/api/hooks";

// DevicePage is the per-device view: the static record, live presence, and
// the mTLS cert expiry for one device, polled every 10s (Issue 18).
export default function DevicePage() {
  const { id } = useParams<{ id: string }>();
  const device = useDevice(id);
  const d = device.data;

  return (
    <main>
      {d && (
        <>
          <h1>{d.hostname}</h1>
          <dl>
            <dt>Client</dt>
            <dd>{d.clientName}</dd>
            <dt>Site</dt>
            <dd>{d.siteName}</dd>
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
