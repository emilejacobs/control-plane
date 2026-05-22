// TanStack Query hooks over the cp-api auth calls — the dashboard's only
// path to server state (Issue 16; no setInterval in components).
"use client";

import { useMutation } from "@tanstack/react-query";
import { firstRun } from "./auth";

interface Credentials {
  email: string;
  password: string;
}

// useFirstRun claims the first-run admin account.
export function useFirstRun() {
  return useMutation({
    mutationFn: ({ email, password }: Credentials) => firstRun(email, password),
  });
}
