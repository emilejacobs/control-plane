import type { ReactNode } from "react";
import type { Viewport } from "next";
import Providers from "./providers";
import "./globals.css";

export const metadata = {
  title: "uknomi · Control Plane",
};

// Matches the design-token grid: the prototype targets a 1240px desktop
// width; the page shell caps at 1280px and the topbar at the same.
export const viewport: Viewport = { width: 1240 };

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
