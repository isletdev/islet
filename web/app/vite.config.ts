import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

const repoRoot = fileURLToPath(new URL("../..", import.meta.url));
const brand = fileURLToPath(new URL("../../brand", import.meta.url));

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@brand": brand, "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: {
    fs: { allow: [repoRoot] },
    // The daemon serves the API; in dev, Vite proxies to it.
    proxy: { "/api": { target: "http://127.0.0.1:9443", changeOrigin: false } },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    target: "es2022",
  },
});
