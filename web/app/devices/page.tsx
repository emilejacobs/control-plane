"use client";

import { useDevices } from "../../lib/api/hooks";

// DevicesPage is the post-login landing shell. Issue 16 ships the empty
// state; the fleet view — grouping by client/site, online indicators — is
// Issue 17.
export default function DevicesPage() {
  const devices = useDevices();

  return (
    <main>
      <h1>Devices</h1>
      {devices.isPending && <p>Loading…</p>}
      {devices.isError && <p role="alert">Could not load devices.</p>}
      {devices.data?.length === 0 && <p>No devices yet.</p>}
      {devices.data && devices.data.length > 0 && (
        <ul>
          {devices.data.map((d) => (
            <li key={d.deviceId}>{d.hostname}</li>
          ))}
        </ul>
      )}
    </main>
  );
}
