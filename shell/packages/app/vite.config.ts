import babel from "@rolldown/plugin-babel";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { engineProxy } from "./src/dev-server/engine-proxy.js";

export default defineConfig({
  // Tenants resolve from the host, so the dev server answers <tenant>.localhost.
  server: {
    allowedHosts: [".localhost"],
    proxy: engineProxy(process.env.GOERP_ENGINE_URL ?? "http://localhost:8080"),
  },
  // maplibre-gl and @duckdb/duckdb-wasm each load their own worker via a
  // URL Vite's dep pre-bundling breaks in dev mode (the worker chunk
  // 404s) — excluding them from optimization keeps their own
  // worker-loading logic intact.
  optimizeDeps: { exclude: ["maplibre-gl", "@duckdb/duckdb-wasm"] },
  plugins: [
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
      routesDirectory: "./src/router/routes",
      generatedRouteTree: "./src/router/routeTree.gen.ts",
    }),
    react(),
    babel({ presets: [reactCompilerPreset()] }),
    tailwindcss(),
  ],
});
