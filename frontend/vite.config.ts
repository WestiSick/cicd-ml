import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  server: {
    host: "0.0.0.0",
    port: 5173,
    strictPort: true,
    // The frontend dev server talks to the api-gateway directly on :8080.
    // CORS is handled there; this proxy block is only here in case we want
    // to flip to same-origin during local debugging.
  },
});
