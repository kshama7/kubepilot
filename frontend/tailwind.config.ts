import type { Config } from "tailwindcss";

// Dark, terminal-adjacent palette. No decorative gradients — this is internal
// infra tooling, not a SaaS landing page.
const config: Config = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        ink: {
          900: "#0a0e14", // page background
          800: "#0f141b", // panels
          700: "#161c26", // raised panels / hover
          600: "#1e2632", // borders
        },
        fg: {
          DEFAULT: "#c9d4e3",
          muted: "#7d8aa0",
          faint: "#566173",
        },
        sev: {
          critical: "#f0506e",
          warning: "#e6a23c",
          info: "#4aa8ff",
          ok: "#3ecf8e",
        },
      },
      fontFamily: {
        mono: [
          "ui-monospace",
          "SFMono-Regular",
          "Menlo",
          "Monaco",
          "Consolas",
          "monospace",
        ],
      },
    },
  },
  plugins: [],
};

export default config;
