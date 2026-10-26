import type { Metadata } from "next";
import { Providers } from "@/components/providers";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "JP Cloud Platform — Self-Hosted Console",
    template: "%s · JP Cloud",
  },
  description: "Enterprise self-hosted cloud platform for modern containers and apps.",
  icons: {
    icon: "/icon.svg",
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className="h-full antialiased">
      <body className="min-h-full font-[family-name:var(--font-body)] bg-[var(--paper)] text-[var(--ink)]">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
