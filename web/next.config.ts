import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Emit a self-contained .next/standalone tree so the Docker runtime stage
  // can ship without the full node_modules. Only affects `next build`.
  output: "standalone",
};

export default nextConfig;
