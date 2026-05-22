import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Vitest runs the dashboard's component and flow tests in jsdom. cp-api is
// mocked at the network layer with MSW — no real backend (Issue 16).
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    setupFiles: ["./vitest.setup.ts"],
    css: false,
  },
});
