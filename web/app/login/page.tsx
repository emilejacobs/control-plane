"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useLogin } from "../../lib/api/hooks";

// LoginPage authenticates an operator. The second factor is a TOTP code, or
// — for an operator who lost their device — a recovery code. A successful
// login routes to the fleet, unless cp-api reports TOTP enrollment is still
// required.
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
    <main>
      <h1>Sign in</h1>
      <form onSubmit={onSubmit}>
        <label>
          Email
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
        </label>
        <label>
          Password
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </label>
        <label>
          {useRecovery ? "Recovery code" : "Authenticator code"}
          <input value={code} onChange={(e) => setCode(e.target.value)} required />
        </label>
        <button type="submit" disabled={loginMut.isPending}>
          Sign in
        </button>
      </form>
      <button type="button" onClick={() => setUseRecovery((v) => !v)}>
        {useRecovery ? "Use an authenticator code" : "Use a recovery code instead"}
      </button>
      {loginMut.isError && <p role="alert">Sign-in failed.</p>}
    </main>
  );
}
