"use client";

import { PlaceholderPage } from "../../components/ui/PlaceholderPage";

// /events — Phase 2. The event stream (heartbeats, cert rotations, operator
// actions, service state changes) lands when the events surface comes
// online. The nav slot is already in place so deep-links from runbooks /
// alarm messages keep working.
export default function EventsPage() {
  return (
    <PlaceholderPage
      title="Events"
      subtitle="Stream of heartbeats, cert rotations, operator actions, and service state changes."
      phase="Phase 2"
    />
  );
}
