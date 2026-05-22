// TanStack Query hooks over the cp-api auth calls — the dashboard's only
// path to server state (Issue 16; no setInterval in components).
"use client";

import { useMutation } from "@tanstack/react-query";
import { firstRun, login, enrollTotp, type LoginInput } from "./auth";

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

// useLogin authenticates an operator; its result carries the
// requires_totp_enrollment flag the login page branches on.
export function useLogin() {
  return useMutation({
    mutationFn: (input: LoginInput) => login(input),
  });
}

// useEnrollTotp mints the TOTP secret + recovery codes for the operator.
export function useEnrollTotp() {
  return useMutation({
    mutationFn: () => enrollTotp(),
  });
}
