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
import { currentTokens } from "../lib/api/client";

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

  if (state !== "ok") return null;
  return <>{children}</>;
}
