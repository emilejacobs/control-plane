"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useLogin } from "../../lib/api/hooks";

// LoginPage authenticates an operator. The second factor is a TOTP code, or
// — for an operator who lost their device — a recovery code. A successful
// login routes to the fleet, unless cp-api reports TOTP enrollment is still
// required.
//
// Single-step shape preserved (form labels still match existing tests);
// design tokens applied via the .auth-shell + .auth-card classes.
export default function LoginPage() {
  const router = useRouter();
  const loginMut = useLogin();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [useRecovery, setUseRecovery] = useState(false);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const input = useRecovery
      ? { email, password, recoveryCode: code }
      : { email, password, totpCode: code };
    loginMut.mutate(input, {
      onSuccess: (result) =>
        router.push(result.requiresTotpEnrollment ? "/totp-enroll" : "/devices"),
    });
  }

  return (
    <main className="auth-shell">
      <div className="auth-card">
        <Brand />
        <h1 className="auth-title">Sign in</h1>
        <p className="auth-sub">Use your operator account to access the fleet.</p>
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
              autoComplete="current-password"
            />
          </label>
          <label className="field">
            <span className="label">{useRecovery ? "Recovery code" : "Authenticator code"}</span>
            <input
              className="input mono"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              required
              autoComplete="one-time-code"
              inputMode={useRecovery ? "text" : "numeric"}
            />
          </label>
          <button
            type="submit"
            className="btn primary"
            disabled={loginMut.isPending}
            style={{ height: 40, justifyContent: "center" }}
          >
            {loginMut.isPending ? "Signing in…" : "Sign in"}
          </button>
        </form>
        <button
          type="button"
          className="btn ghost"
          onClick={() => setUseRecovery((v) => !v)}
          style={{ marginTop: 12, width: "100%", justifyContent: "center" }}
        >
          {useRecovery ? "Use an authenticator code" : "Use a recovery code instead"}
        </button>
        {loginMut.isError && (
          <p role="alert" className="pill red" style={{ marginTop: 16, display: "inline-flex" }}>
            Sign-in failed.
          </p>
        )}
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
