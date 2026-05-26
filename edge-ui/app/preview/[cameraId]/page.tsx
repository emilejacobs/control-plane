// Server component shell that owns generateStaticParams (required
// by output: 'export' for dynamic segments) and unwraps the params
// promise before handing the resolved cameraId to the client
// component. The client component (PreviewClient) owns the actual
// rendering — it must be a client component because <img> with a
// multipart/x-mixed-replace src is a browser-side concern only.
import PreviewClient from "./preview-client";

// Static export requires generateStaticParams() for dynamic segments.
// We emit a single placeholder so `next build` succeeds; at runtime
// the Go static handler serves index.html for any /preview/<id> via
// the SPA fallback, and the client component picks up the real
// cameraId from the URL on the client.
export function generateStaticParams() {
  return [{ cameraId: "_" }];
}

export default async function PreviewPage({
  params,
}: {
  params: Promise<{ cameraId: string }>;
}) {
  const { cameraId } = await params;
  return <PreviewClient cameraId={cameraId} />;
}
