import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";

/**
 * Vitest runs through the Vite config that already exists: the same transform, the same ESM
 * handling, the same alias. Jest would need its own of each, describing the same project.
 */
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { "@app": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
});
