import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@":                     path.resolve(__dirname, "./src"),
      "@privasync/i18n":       path.resolve(__dirname, "../../packages/i18n/src/index.ts"),
      "@privasync/ui":         path.resolve(__dirname, "../../packages/ui/src/index.ts"),
      "@privasync/api-client": path.resolve(__dirname, "../../packages/api-client/src/index.ts"),
    },
  },
  // Tauri dev server
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
  },
  envPrefix: ["VITE_", "TAURI_"],
  build: {
    target: ["es2021", "chrome100", "safari13"],
    minify: !process.env.TAURI_DEBUG,
    sourcemap: !!process.env.TAURI_DEBUG,
    outDir: "dist",
  },
});
