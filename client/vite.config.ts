import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { loadEnv } from "vite";
import { defineConfig } from "vitest/config";

const repoRoot = fileURLToPath(new URL("..", import.meta.url));

function serverUrl(mode: string): string {
  const fromProc = process.env.MODUPDATER_SERVER_URL?.trim() ?? "";
  const fromFile = loadEnv(mode, repoRoot, "MODUPDATER_").MODUPDATER_SERVER_URL?.trim() ?? "";
  const raw = (fromProc || fromFile || "http://127.0.0.1:8080").replace(/\/+$/, "");
  return raw;
}

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
  },
  envPrefix: ["VITE_", "TAURI_"],
  define: {
    __MODUPDATER_SERVER_URL__: JSON.stringify(serverUrl(mode)),
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
}));
