import { defineConfig } from "vitest/config";
import { moduleDevelopment } from "./src/dev-server/module-development.js";

export default defineConfig({
  plugins: [moduleDevelopment()],
  test: {
    // One project per environment, split by which files need a DOM —
    // mirrors @goerp/sdk's own vitest.config.ts split.
    projects: [
      {
        extends: true,
        test: { name: "unit", environment: "node", include: ["src/**/*.test.ts"] },
      },
      {
        extends: true,
        test: { name: "component", environment: "jsdom", include: ["src/**/*.test.tsx"] },
      },
    ],
  },
});
