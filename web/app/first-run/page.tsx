"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useFirstRun } from "../../lib/api/hooks";
import { Logo } from "../../components/ui/Logo";

// FirstRunPage claims the first-run admin account. cp-api still gates this
// server-side (410 once initialized); on success the operator is routed
// straight into mandatory TOTP enrollment.
//
// Single-step shape preserved (form labels + "Create account" button still
// match the existing tests).
export default function FirstRunPage() {
  const router = useRouter();
  const firstRun = useFirstRun();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    firstRun.mutate(
      { email, password },
      { onSuccess: () => router.push("/totp-enroll") },
    );
  }

  return (
    <main className="auth-shell">
      <div className="auth-card" style={{ maxWidth: 460 }}>
        <Brand />
        <h1 className="auth-title">Create the first admin account</h1>
        <p className="auth-sub">
          This bootstraps your control plane. You only do this once.
        </p>
        <form className="auth-form" onSubmit={onSubmit}>
          <label className="field">
            <span className="label">Email</span>
            <input
              className="input"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              autoComplete="email"
            />
          </label>
          <label className="field">
            <span className="label">Password</span>
            <input
              className="input"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              autoComplete="new-password"
            />
            <div className="hint">
              Minimum 12 characters; include mixed case, a digit, and a symbol.
            </div>
          </label>
          <button
            type="submit"
            className="btn primary"
            disabled={firstRun.isPending}
            style={{ height: 40, justifyContent: "center" }}
          >
            {firstRun.isPending ? "Creating…" : "Create account"}
          </button>
        </form>
        {firstRun.isError && (
          <p role="alert" className="pill red" style={{ marginTop: 16, display: "inline-flex" }}>
            Could not create the account.
          </p>
        )}
      </div>
    </main>
  );
}

function Brand() {
  return (
    <div className="auth-brand">
      <Logo />
      <span>uknomi</span>
      <span style={{ color: "var(--ink-3)", fontWeight: 500 }}>Control Plane</span>
    </div>
  );
}
