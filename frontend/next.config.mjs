/** @type {import('next').NextConfig} */

// The dashboard talks to the Go API through a same-origin proxy, so the browser
// never makes a cross-origin request and no CORS config is needed on the backend.
// Point KUBEPILOT_API_URL at the API (defaults to the local dev server).
const apiURL = process.env.KUBEPILOT_API_URL || "http://localhost:8080";

const nextConfig = {
  reactStrictMode: true,
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${apiURL}/api/:path*`,
      },
    ];
  },
};

export default nextConfig;
