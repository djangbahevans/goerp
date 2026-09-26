import { fileURLToPath } from "node:url";
import { storybookTest } from "@storybook/addon-vitest/vitest-plugin";
import { playwright } from "@vitest/browser-playwright";
import { defineConfig } from "vitest/config";

// Runs every story in the catalog as a test: render, then its play function,
// in headless Chromium — the real-browser checks jsdom can't make.
export default defineConfig({
  plugins: [storybookTest({ configDir: fileURLToPath(new URL(".storybook", import.meta.url)) })],
  test: {
    name: "storybook",
    browser: {
      enabled: true,
      headless: true,
      provider: playwright(),
      instances: [{ browser: "chromium" }],
    },
  },
});
