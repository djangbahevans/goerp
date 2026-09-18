import babel from "@rolldown/plugin-babel";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
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
