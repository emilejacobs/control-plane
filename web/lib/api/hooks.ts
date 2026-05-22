// TanStack Query hooks over the cp-api auth calls — the dashboard's only
// path to server state (Issue 16; no setInterval in components).
"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { firstRun, login, enrollTotp, type LoginInput } from "./auth";
import { getDevices } from "./devices";

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

// useDevices loads the operator's site-scoped fleet. The fleet view (#17)
// builds on it; #16 ships only the empty Devices shell.
export function useDevices() {
  return useQuery({
    queryKey: ["devices"],
    queryFn: getDevices,
  });
}
