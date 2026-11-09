import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwind from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

export default defineConfig({
  plugins: [react(), tailwind()],
  // Named, so an import says where a module is rather than where the importer sits.
  resolve: {
    alias: { "@app": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // Everything the app talks to is this origin; in development that origin is the Go server.
    proxy: {
      "/api": "http://127.0.0.1:80",
    },
  },
});
