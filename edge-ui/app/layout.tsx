// Root layout for the Edge UI Next.js app. The static export
// (next.config.ts -> output: 'export') emits index.html out of this
// layout, which the Go binary in cmd/uknomi-edge-ui embeds and serves.
//
// No global navigation: Edge UI has exactly two surfaces in v1
// (Camera live preview, Audio test next slice) and both are reached
// by deep-link from CP — no menu chrome needed.
export const metadata = {
  title: "uKnomi Edge",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body
        style={{
          fontFamily:
            "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
          margin: 0,
          padding: 0,
          background: "#0a0a0a",
          color: "#f0f0f0",
        }}
      >
        {children}
      </body>
    </html>
  );
}
