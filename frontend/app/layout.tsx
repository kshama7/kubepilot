import type { Metadata } from "next";
import "./globals.css";
import { ClusterProvider } from "@/components/ClusterContext";
import { Sidebar } from "@/components/Sidebar";
import { TopBar } from "@/components/TopBar";

export const metadata: Metadata = {
  title: "KubePilot",
  description: "Kubernetes Reliability Platform — deterministic, rule-based analysis",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body>
        <ClusterProvider>
          <div className="flex h-screen overflow-hidden">
            <Sidebar />
            <div className="flex min-w-0 flex-1 flex-col">
              <TopBar />
              <main className="flex-1 overflow-y-auto px-6 py-6">
                {children}
              </main>
            </div>
          </div>
        </ClusterProvider>
      </body>
    </html>
  );
}
