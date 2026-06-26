"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

interface NavItem {
  href: string;
  label: string;
  glyph: string;
}

interface NavGroup {
  title: string;
  items: NavItem[];
}

const GROUPS: NavGroup[] = [
  {
    title: "Overview",
    items: [
      { href: "/", label: "Overview", glyph: "◎" },
      { href: "/recommendations", label: "Recommendations", glyph: "★" },
    ],
  },
  {
    title: "Inventory",
    items: [
      { href: "/clusters", label: "Clusters", glyph: "◆" },
      { href: "/nodes", label: "Nodes", glyph: "▮" },
      { href: "/namespaces", label: "Namespaces", glyph: "❑" },
    ],
  },
  {
    title: "Analysis",
    items: [
      { href: "/workloads", label: "Workloads", glyph: "▣" },
      { href: "/resources", label: "Resources", glyph: "▤" },
      { href: "/reliability", label: "Reliability", glyph: "▦" },
      { href: "/gitops", label: "GitOps", glyph: "◈" },
      { href: "/upgrade", label: "Upgrade Advisor", glyph: "▲" },
      { href: "/security", label: "Security", glyph: "⬡" },
      { href: "/capacity", label: "Capacity", glyph: "▮" },
    ],
  },
  {
    title: "System",
    items: [{ href: "/settings", label: "Settings", glyph: "⚙" }],
  },
];

export function Sidebar() {
  const pathname = usePathname();
  return (
    <nav className="flex h-full w-56 shrink-0 flex-col border-r border-ink-600 bg-ink-800">
      <div className="flex items-center gap-2 border-b border-ink-600 px-4 py-4">
        <span className="text-sev-info">◆</span>
        <span className="text-sm font-semibold tracking-wide text-fg">
          KubePilot
        </span>
      </div>
      <div className="flex-1 overflow-y-auto py-3">
        {GROUPS.map((g) => (
          <div key={g.title} className="mb-4">
            <div className="px-4 pb-1 text-[10px] uppercase tracking-widest text-fg-faint">
              {g.title}
            </div>
            {g.items.map((item) => {
              const active =
                item.href === "/"
                  ? pathname === "/"
                  : pathname.startsWith(item.href);
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  className={`flex items-center gap-2.5 px-4 py-1.5 text-sm ${
                    active
                      ? "border-l-2 border-sev-info bg-ink-700 text-fg"
                      : "border-l-2 border-transparent text-fg-muted hover:bg-ink-700/50 hover:text-fg"
                  }`}
                >
                  <span className="w-4 text-center text-xs text-fg-faint">
                    {item.glyph}
                  </span>
                  {item.label}
                </Link>
              );
            })}
          </div>
        ))}
      </div>
      <div className="border-t border-ink-600 px-4 py-3 text-[10px] text-fg-faint">
        deterministic-first · AI explains
      </div>
    </nav>
  );
}
