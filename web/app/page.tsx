// Home — the authenticated landing. Mirrors /overview so a deep-link to
// the root drops the operator directly into the fleet-health dashboard
// without a redirect bounce.
import OverviewPage from "./overview/page";

export default function Home() {
  return <OverviewPage />;
}
