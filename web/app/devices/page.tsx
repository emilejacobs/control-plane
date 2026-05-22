"use client";

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
      {devices.isPending && <p>Loading…</p>}
      {devices.isError && <p role="alert">Could not load devices.</p>}
      {devices.data?.length === 0 && <p>No devices yet.</p>}
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
                      <PresenceChip online={d.isOnline} /> {d.hostname}
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
