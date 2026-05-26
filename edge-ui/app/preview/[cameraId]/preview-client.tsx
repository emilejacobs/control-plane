"use client";

import { useEffect, useState } from "react";

// PreviewClient is the client component the static-export page mounts
// for /preview/<camera_id>. It reads cameraId from props (server
// component resolved the params promise) but overrides with the
// runtime URL when the SPA fallback served index.html for an
// unknown-at-build-time cameraId — that's why the build-time
// generateStaticParams returns just `_`.
//
// <img> with a multipart/x-mixed-replace src renders MJPEG natively
// in every modern browser; no need for media-element streaming or a
// custom decoder.
export default function PreviewClient({ cameraId }: { cameraId: string }) {
  // Default to the prop, then refine from the URL on mount so the
  // SPA-fallback path (Go serves index.html for /preview/cam42) ends
  // up with the right camera_id rather than the build-time "_" placeholder.
  const [resolvedId, setResolvedId] = useState(cameraId);
  useEffect(() => {
    const m = window.location.pathname.match(/^\/preview\/([^/]+)/);
    if (m && m[1] !== "_" && m[1] !== resolvedId) {
      setResolvedId(m[1]);
    }
    // resolvedId is stable after first mount; intentional one-shot.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <main
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: 12,
        padding: 16,
        minHeight: "100vh",
        background: "#0a0a0a",
        color: "#f0f0f0",
      }}
    >
      <header style={{ fontSize: 13, color: "#888" }}>
        Camera live preview ·{" "}
        <span style={{ fontFamily: "ui-monospace, monospace" }}>{resolvedId}</span>
      </header>
      <img
        src={`/preview/${resolvedId}/stream`}
        alt={`Live preview of ${resolvedId}`}
        style={{
          maxWidth: "100%",
          maxHeight: "85vh",
          objectFit: "contain",
          background: "#000",
          border: "1px solid #222",
        }}
      />
    </main>
  );
}
