import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The production build lands where serendipd embeds it
// (cmd/serendipd/console). `vite dev` proxies the API and login routes to a
// local serendipd; the strict CSP applies only to the embedded build.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../cmd/serendipd/console/dist/app",
    emptyOutDir: true,
    sourcemap: false,
    // No inline <script> or data: module preload polyfill: the CSP is
    // script-src 'self'.
    modulePreload: { polyfill: false },
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      "/v1": "http://localhost:8080",
      "/auth": "http://localhost:8080",
    },
  },
  test: {
    environment: "jsdom",
  },
});
