import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev, the BFF runs on :8088 and serves /api and /config.json.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/api": "http://localhost:8088", "/config.json": "http://localhost:8088" },
  },
});
