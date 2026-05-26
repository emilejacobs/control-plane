// Root placeholder. The Edge UI is reached via deep-links from CP
// (e.g. /preview/<camera_id>) and the index page only renders if an
// operator types the bare hostname into a browser — keep it minimal
// but clearly identify the service so a misclick is debuggable.
export default function Home() {
  return (
    <main style={{ padding: 24 }}>
      <h1 style={{ fontSize: 18, fontWeight: 600, margin: 0 }}>uKnomi Edge</h1>
      <p style={{ marginTop: 8, fontSize: 13, color: "#999" }}>
        Device-local Edge UI. Open a Camera live preview from CP via the
        "Verify angle" button on the Cameras panel.
      </p>
    </main>
  );
}
