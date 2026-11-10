import type { Metadata } from "next";
import type { ReactNode } from "react";
import { I18nProvider } from "@invoyis/ui";
import { serverLocale } from "../lib/locale-server";
import "@invoyis/ui/base.css";
export const metadata: Metadata = { title: "Invoyis" };
export default async function RootLayout({ children }: { children: ReactNode }) {
  const locale = await serverLocale();
  return <html lang={locale} dir={locale === 'he' ? 'rtl' : 'ltr'}><body><I18nProvider locale={locale}>{children}</I18nProvider></body></html>;
}
