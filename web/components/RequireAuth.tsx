// RequireAuth — client-side auth gate for protected pages. Mounted at the
// top of every authenticated page so a direct navigation with no token
// pair bounces to /login instead of rendering the shell behind silent
// 401s. The check runs in a useEffect (currentTokens reads localStorage,
// which is only populated on the client), and children are withheld until
// the check has decided — no flash of the protected UI on a blocked
// navigation.
"use client";

import { useEffect, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { currentTokens, SESSION_EXPIRED_EVENT } from "../lib/api/client";

type GateState = "checking" | "ok";

interface Props {
  children: ReactNode;
}

export function RequireAuth({ children }: Props) {
  const router = useRouter();
  const [state, setState] = useState<GateState>("checking");

  useEffect(() => {
    if (currentTokens() === null) {
      router.replace("/login");
      return;
    }
    setState("ok");
  }, [router]);

  // The mount check above only fires on (re)navigation. If the session dies
  // while the operator sits here — a background poll's token refresh is
  // rejected — bounce to /login at once instead of waiting for their next click.
  useEffect(() => {
    const onExpired = () => router.replace("/login");
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
  }, [router]);

  if (state !== "ok") return null;
  return <>{children}</>;
}
