import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? process.env.VITE_API_BASE_URL ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      "/api": apiProxyTarget,
      "/healthz": apiProxyTarget,
      "/readyz": apiProxyTarget
    }
  }
});
