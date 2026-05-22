"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useFirstRun } from "../../lib/api/hooks";

// FirstRunPage claims the first-run admin account. cp-api still gates this
// server-side (410 once initialized); on success the operator is routed
// straight into mandatory TOTP enrollment.
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
    <main>
      <h1>Create the first admin account</h1>
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
        <button type="submit" disabled={firstRun.isPending}>
          Create account
        </button>
      </form>
      {firstRun.isError && <p role="alert">Could not create the account.</p>}
    </main>
  );
}
