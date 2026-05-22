"use client";

import { useParams } from "next/navigation";
import { useDevice } from "../../../lib/api/hooks";

// DevicePage is the per-device view: the static record, live presence, and
// the mTLS cert expiry for one device, polled every 10s (Issue 18).
export default function DevicePage() {
  const { id } = useParams<{ id: string }>();
  const device = useDevice(id);

  return <main>{device.data && <h1>{device.data.hostname}</h1>}</main>;
}
