"use client";

import { useEffect } from "react";

// SnapshotLightbox shows a full-size snapshot in a fixed overlay (#8/#9). Close
// via the button, clicking the backdrop, or Escape. Reused by the single-shot
// enlarge and the history modal's per-row enlarge.
interface Props {
  url: string;
  alt: string;
  onClose: () => void;
}

export function SnapshotLightbox({ url, alt, onClose }: Props) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={alt}
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.8)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 1100,
        cursor: "zoom-out",
        padding: 24,
      }}
    >
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={url}
        alt={alt}
        onClick={(e) => e.stopPropagation()}
        style={{
          maxWidth: "92vw",
          maxHeight: "92vh",
          objectFit: "contain",
          borderRadius: 4,
          boxShadow: "0 12px 40px rgba(0,0,0,0.5)",
          cursor: "default",
        }}
      />
      <button
        type="button"
        onClick={onClose}
        aria-label="Close"
        style={{
          position: "fixed",
          top: 16,
          right: 16,
          background: "rgba(0,0,0,0.5)",
          color: "#fff",
          border: "1px solid rgba(255,255,255,0.4)",
          borderRadius: 4,
          padding: "6px 12px",
          fontSize: 13,
          cursor: "pointer",
        }}
      >
        Close
      </button>
    </div>
  );
}
