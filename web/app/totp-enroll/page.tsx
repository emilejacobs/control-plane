"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { QRCodeSVG } from "qrcode.react";
import { useEnrollTotp } from "../../lib/api/hooks";
import { RequireAuth } from "../../components/RequireAuth";

// TotpEnrollPage runs the mandatory one-time TOTP enrollment: it mints the
// secret, renders it as a QR code, and shows the recovery codes once. The
// operator cannot continue until they confirm the codes are saved.
//
// Restyled with auth-shell + auth-card; the button names + aria labels
// + recovery-code checkbox text are preserved verbatim for the existing
// auth-flow test.
export default function TotpEnrollPage() {
  return (
    <RequireAuth>
      <TotpEnrollBody />
    </RequireAuth>
  );
}

function TotpEnrollBody() {
  const router = useRouter();
  const enroll = useEnrollTotp();
  const [savedCodes, setSavedCodes] = useState(false);

  if (!enroll.data) {
    return (
      <main className="auth-shell">
        <div className="auth-card" style={{ maxWidth: 440 }}>
          <Brand />
          <h1 className="auth-title">Set up two-factor authentication</h1>
          <p className="auth-sub">
            uKnomi requires an authenticator app for every operator account.
          </p>
          <button
            className="btn primary"
            onClick={() => enroll.mutate()}
            disabled={enroll.isPending}
            style={{ height: 40, width: "100%", justifyContent: "center" }}
          >
            {enroll.isPending ? "Generating…" : "Generate authenticator setup"}
          </button>
          {enroll.isError && (
            <p
              role="alert"
              className="pill red"
              style={{ marginTop: 16, display: "inline-flex" }}
            >
              Could not start enrollment.
            </p>
          )}
        </div>
      </main>
    );
  }

  return (
    <main className="auth-shell">
      <div className="auth-card" style={{ maxWidth: 460 }}>
        <Brand />
        <h1 className="auth-title">Scan this with your authenticator app</h1>
        <p className="auth-sub">
          Then save the recovery codes below — each works only once if you
          lose access to your authenticator.
        </p>

        <figure
          aria-label="Authenticator QR code"
          className="qr-wrap"
          style={{ margin: "0 0 18px" }}
        >
          <QRCodeSVG value={enroll.data.provisioningUri} size={168} />
        </figure>

        <label className="field" style={{ marginBottom: 18 }}>
          <span className="label">Or enter the secret manually</span>
          <code
            className="input mono"
            style={{
              display: "flex",
              alignItems: "center",
              wordBreak: "break-all",
              minHeight: 36,
              fontSize: 11.5,
              padding: "8px 12px",
            }}
          >
            {enroll.data.provisioningUri}
          </code>
        </label>

        <h2
          className="card-section-label"
          style={{ marginTop: 18, marginBottom: 10 }}
        >
          Recovery codes
        </h2>
        <p className="muted" style={{ fontSize: 13, marginTop: 0 }}>
          Save these somewhere safe — each one works only once.
        </p>
        <ul
          className="mono"
          style={{
            background: "var(--bg-tinted)",
            borderRadius: "var(--r-md)",
            padding: "12px 16px",
            margin: "8px 0 16px",
            listStyle: "none",
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "6px 16px",
            fontSize: 12.5,
          }}
        >
          {enroll.data.recoveryCodes.map((code) => (
            <li key={code}>{code}</li>
          ))}
        </ul>

        <label
          className="row"
          style={{ gap: 10, fontSize: 13, marginBottom: 16, cursor: "pointer" }}
        >
          <input
            type="checkbox"
            checked={savedCodes}
            onChange={(e) => setSavedCodes(e.target.checked)}
          />
          <span>I have saved my recovery codes</span>
        </label>

        <button
          className="btn primary"
          disabled={!savedCodes}
          onClick={() => router.push("/login")}
          style={{ height: 40, width: "100%", justifyContent: "center" }}
        >
          Continue
        </button>
      </div>
    </main>
  );
}

function Brand() {
  return (
    <div className="auth-brand">
      <span className="topbar-logo" aria-hidden>
        <svg width={14} height={14} viewBox="0 0 14 14" fill="none">
          <path
            d="M3.5 6.5c.7 1.6 2 2.6 3.5 2.6s2.8-1 3.5-2.6"
            stroke="currentColor"
            strokeWidth={1.6}
            strokeLinecap="round"
          />
        </svg>
      </span>
      <span>uknomi</span>
      <span style={{ color: "var(--ink-3)", fontWeight: 500 }}>Control Plane</span>
    </div>
  );
}
