"use client";

import Link from "next/link";
import { useDevices } from "../../lib/api/hooks";
import { groupDevices } from "../../lib/fleet";
import { PresenceChip } from "../../components/PresenceChip";

// DevicesPage is the fleet view: every device the operator may see, grouped
// by client and site, polled so presence stays current (Issue 17).
export default function DevicesPage() {
  const devices = useDevices();

  return (
    <main>
      <h1>Devices</h1>
      {devices.isPending && <p role="status">Loading devices…</p>}
      {devices.isError && (
        <div role="alert">
          <p>Could not load devices.</p>
          <button onClick={() => devices.refetch()}>Refresh</button>
        </div>
      )}
      {devices.data?.length === 0 && (
        <p>No devices yet — none are enrolled in the sites you can see.</p>
      )}
      {devices.data &&
        groupDevices(devices.data).map((client) => (
          <section key={client.clientName}>
            <h2>{client.clientName}</h2>
            {client.sites.map((site) => (
              <section key={site.siteName}>
                <h3>{site.siteName}</h3>
                <ul>
                  {site.devices.map((d) => (
                    <li key={d.deviceId}>
                      <PresenceChip online={d.isOnline} />{" "}
                      <Link href={`/devices/${d.deviceId}`}>{d.hostname}</Link>
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </section>
        ))}
    </main>
  );
}
