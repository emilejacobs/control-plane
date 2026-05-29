"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useSetPassword } from "../../lib/api/hooks";
import { RequireAuth } from "../../components/RequireAuth";
import { Logo } from "../../components/ui/Logo";

// SetPasswordPage is the constrained set-new-password step (#16) an operator
// on a system-generated temp password must complete on first login, before
// TOTP enrollment. The minimum length mirrors the server's floor (12).
const MIN_LENGTH = 12;

export default function SetPasswordPage() {
  return (
    <RequireAuth>
      <SetPasswordBody />
    </RequireAuth>
  );
}

function SetPasswordBody() {
  const router = useRouter();
  const setPw = useSetPassword();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");

  const tooShort = password.length > 0 && password.length < MIN_LENGTH;
  const mismatch = confirm.length > 0 && confirm !== password;
  const canSubmit = password.length >= MIN_LENGTH && confirm === password && !setPw.isPending;

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setPw.mutate(password, {
      // After rotating, continue the onboarding chain into TOTP enrollment.
      onSuccess: () => router.push("/totp-enroll"),
    });
  }

  return (
    <main className="auth-shell">
      <div className="auth-card" style={{ maxWidth: 440 }}>
        <Brand />
        <h1 className="auth-title">Choose a new password</h1>
        <p className="auth-sub">
          Your account was created with a temporary password. Set a new one to
          continue.
        </p>
        <form className="auth-form" onSubmit={onSubmit}>
          <label className="field">
            <span className="label">New password</span>
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-label="New password"
            />
          </label>
          <label className="field">
            <span className="label">Confirm new password</span>
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              aria-label="Confirm new password"
            />
          </label>

          {tooShort && (
            <p className="muted" style={{ fontSize: 13, margin: 0 }}>
              Use at least {MIN_LENGTH} characters.
            </p>
          )}
          {mismatch && (
            <p role="alert" className="pill red" style={{ display: "inline-flex" }}>
              Passwords don’t match.
            </p>
          )}
          {setPw.isError && (
            <p role="alert" className="pill red" style={{ display: "inline-flex" }}>
              Could not set password.
            </p>
          )}

          <button
            className="btn primary"
            type="submit"
            disabled={!canSubmit}
            style={{ height: 40, width: "100%", justifyContent: "center" }}
          >
            {setPw.isPending ? "Saving…" : "Set password"}
          </button>
        </form>
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
