import type { NextConfig } from "next";

// Emit a fully static HTML/JS bundle so the Go binary in
// cmd/uknomi-edge-ui can embed it via //go:embed. Dynamic route
// resolution (e.g. /preview/<camera_id>) happens client-side; the
// Go static handler serves index.html as the SPA fallback.
const nextConfig: NextConfig = {
  output: "export",
  // Static export must not append trailing slashes when the binary
  // serves files — the Go embed maps URL paths to file paths and a
  // trailing slash creates an off-by-one. Keep the default off.
  images: { unoptimized: true },
};

export default nextConfig;
