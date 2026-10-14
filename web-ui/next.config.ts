import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Serve web UI under /webui path
  basePath: '/webui',
  assetPrefix: '/webui',
  trailingSlash: true,
  // Enable static export for embedding in Go binary
  output: 'export',
  // Disable image optimization for static export
  images: {
    unoptimized: true,
  },
};

export default nextConfig;
