"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { QRCodeSVG } from "qrcode.react";
import { useEnrollTotp } from "../../lib/api/hooks";

// TotpEnrollPage runs the mandatory one-time TOTP enrollment: it mints the
// secret, renders it as a QR code, and shows the recovery codes once. The
// operator cannot continue until they confirm the codes are saved.
export default function TotpEnrollPage() {
  const router = useRouter();
  const enroll = useEnrollTotp();
  const [savedCodes, setSavedCodes] = useState(false);

  if (!enroll.data) {
    return (
      <main>
        <h1>Set up two-factor authentication</h1>
        <p>uKnomi requires an authenticator app for every operator account.</p>
        <button onClick={() => enroll.mutate()} disabled={enroll.isPending}>
          Generate authenticator setup
        </button>
        {enroll.isError && <p role="alert">Could not start enrollment.</p>}
      </main>
    );
  }

  return (
    <main>
      <h1>Scan this with your authenticator app</h1>
      <figure aria-label="Authenticator QR code">
        <QRCodeSVG value={enroll.data.provisioningUri} />
      </figure>
      <p>
        Or enter this key manually: <code>{enroll.data.provisioningUri}</code>
      </p>

      <h2>Recovery codes</h2>
      <p>Save these somewhere safe — each one works only once.</p>
      <ul>
        {enroll.data.recoveryCodes.map((code) => (
          <li key={code}>{code}</li>
        ))}
      </ul>

      <label>
        <input
          type="checkbox"
          checked={savedCodes}
          onChange={(e) => setSavedCodes(e.target.checked)}
        />
        I have saved my recovery codes
      </label>

      <button disabled={!savedCodes} onClick={() => router.push("/login")}>
        Continue
      </button>
    </main>
  );
}
