"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useLogin } from "../../lib/api/hooks";
import { ApiError, type LoginOutcome } from "../../lib/api/auth";
import { Logo } from "../../components/ui/Logo";

// LoginPage authenticates an operator in two steps:
//   1. email + password only.
//   2. if the account has 2FA, a TOTP code (or recovery code).
// A brand-new operator (temp password, no 2FA yet) authenticates on the
// password alone and is routed straight into set-password → TOTP enrollment,
// so they're never asked for a code they don't have. cp-api distinguishes
// "password OK, 2FA needed" (kind: totpRequired) from bad credentials, which
// is what lets step 1 know whether to advance to step 2.
export default function LoginPage() {
  const router = useRouter();
  const loginMut = useLogin();
  const [step, setStep] = useState<"credentials" | "totp">("credentials");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [useRecovery, setUseRecovery] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Onboarding order (#16): set new password (if on a temp one) → enroll
  // TOTP → the Overview dashboard (the default landing).
  function routeAfterAuth(o: Extract<LoginOutcome, { kind: "authenticated" }>) {
    if (o.mustChangePassword) router.push("/set-password");
    else if (o.requiresTotpEnrollment) router.push("/totp-enroll");
    else router.push("/overview");
  }

  function submitCredentials(e: FormEvent) {
    e.preventDefault();
    setError(null);
    loginMut.mutate(
      { email, password },
      {
        onSuccess: (outcome) => {
          if (outcome.kind === "totpRequired") setStep("totp");
          else routeAfterAuth(outcome);
        },
        onError: (err) =>
          setError(
            err instanceof ApiError && err.status === 429
              ? "Too many attempts. Try again in a few minutes."
              : "Invalid email or password.",
          ),
      },
    );
  }

  function submitTotp(e: FormEvent) {
    e.preventDefault();
    setError(null);
    const input = useRecovery
      ? { email, password, recoveryCode: code }
      : { email, password, totpCode: code };
    loginMut.mutate(input, {
      onSuccess: (outcome) => {
        // A still-"totpRequired" outcome here means the code was wrong.
        if (outcome.kind === "totpRequired") setError("That code didn't work. Try again.");
        else routeAfterAuth(outcome);
      },
      onError: () => setError("Sign-in failed. Please try again."),
    });
  }

  function backToCredentials() {
    setStep("credentials");
    setCode("");
    setUseRecovery(false);
    setError(null);
  }

  return (
    <main className="auth-shell">
      <div className="auth-card">
        <Brand />
        {step === "credentials" ? (
          <>
            <h1 className="auth-title">Sign in</h1>
            <p className="auth-sub">Use your operator account to access the fleet.</p>
            <form className="auth-form" onSubmit={submitCredentials}>
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
              <button
                type="submit"
                className="btn primary"
                disabled={loginMut.isPending}
                style={{ height: 40, justifyContent: "center" }}
              >
                {loginMut.isPending ? "Checking…" : "Continue"}
              </button>
            </form>
          </>
        ) : (
          <>
            <h1 className="auth-title">Two-factor authentication</h1>
            <p className="auth-sub">
              {useRecovery
                ? "Enter one of your recovery codes."
                : "Enter the 6-digit code from your authenticator app."}
            </p>
            <form className="auth-form" onSubmit={submitTotp}>
              <label className="field">
                <span className="label">{useRecovery ? "Recovery code" : "Authenticator code"}</span>
                <input
                  className="input mono"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  required
                  autoFocus
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
              onClick={() => {
                setUseRecovery((v) => !v);
                setCode("");
                setError(null);
              }}
              style={{ marginTop: 12, width: "100%", justifyContent: "center" }}
            >
              {useRecovery ? "Use an authenticator code" : "Use a recovery code instead"}
            </button>
            <button
              type="button"
              className="btn ghost"
              onClick={backToCredentials}
              style={{ marginTop: 8, width: "100%", justifyContent: "center" }}
            >
              Back
            </button>
          </>
        )}
        {error && (
          <p role="alert" className="pill red" style={{ marginTop: 16, display: "inline-flex" }}>
            {error}
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
