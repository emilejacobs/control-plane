// Logo renders the uKnomi brand mark — the same green smiley used by the
// Edge UI ("Talon") header (mac-mini-rollout/webui/static/img/uknomi-logo.png,
// vendored to /public). Green reads on both the dark topbar and the light
// auth cards, so one asset serves every surface. height defaults to the
// topbar/auth lockup size; width follows the image's aspect ratio.
export function Logo({ height = 22 }: { height?: number }) {
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src="/uknomi-logo.png"
      alt="uKnomi"
      height={height}
      style={{ height, width: "auto", display: "block" }}
    />
  );
}
