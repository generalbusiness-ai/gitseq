import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../internal/service/uidist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/v0": "http://127.0.0.1:7777",
    },
  },
});
