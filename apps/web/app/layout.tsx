import type { Metadata } from "next";
import type { ReactNode } from "react";
import "@invoyis/ui/base.css";

export const metadata: Metadata = {
  title: "Invoyis",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
