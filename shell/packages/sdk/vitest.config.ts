import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // One project per environment, split by which files need a DOM.
    projects: [
      {
        extends: true,
        test: {
          name: "unit",
          environment: "node",
          include: ["src/**/*.test.ts"],
          // cn.test.ts reads the app's tokens.css as ?raw; vitest otherwise
          // replaces every CSS import with an empty string.
          css: { include: [/tokens\.css/] },
        },
      },
      {
        extends: true,
        test: { name: "component", environment: "jsdom", include: ["src/**/*.test.tsx"] },
      },
    ],
  },
});
