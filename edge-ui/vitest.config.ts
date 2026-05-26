import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Edge UI uses the same vitest harness as web/ — jsdom for component
// tests. No MSW (there is no network layer on Edge UI yet; the audio
// test slice may add one).
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    css: false,
  },
});
