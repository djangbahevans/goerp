import { defineConfig } from "vite";

export default defineConfig({
  base: "./",
  build: {
    outDir: "dist-testing",
    rolldownOptions: {
      input: "src/testing/index.ts",
      preserveEntrySignatures: "strict",
      output: { format: "es", entryFileNames: "index.js" },
      external: (id) => !id.includes("?url") && !id.startsWith(".") && !id.startsWith("/") && !id.startsWith("\u0000"),
    },
  },
});
