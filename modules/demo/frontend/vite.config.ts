import { defineConfig } from "vite";

export default defineConfig({
  build: {
    rolldownOptions: {
      external: ["@goerp/sdk/module"],
    },
    lib: {
      entry: "src/index.ts",
      formats: ["es"],
      fileName: () => "bundle.js",
    },
  },
});
