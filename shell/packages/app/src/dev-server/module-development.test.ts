import type { ConfigEnv } from "vite";
import { afterEach, describe, expect, it, vi } from "vitest";
import { moduleDevelopment } from "./module-development.js";

afterEach(() => vi.unstubAllEnvs());

function source(command: ConfigEnv["command"]) {
  const plugin = moduleDevelopment();
  if (typeof plugin.config !== "function" || typeof plugin.load !== "function") throw new Error("missing plugin hooks");
  plugin.config.call({} as never, {}, { command, mode: "development" });
  return plugin.load.call({} as never, "\0virtual:goerp-module-development");
}

describe("module development loading", () => {
  it("keeps local imports out of a production build even with development environment variables", async () => {
    vi.stubEnv("GOERP_MODULE_FRONTEND_NAME", "demo");
    vi.stubEnv("GOERP_MODULE_FRONTEND_ENTRY", "/__goerp_module/src/index.ts");
    expect(await source("build")).not.toContain("/__goerp_module/");
  });

  it("keeps the verified bundle path when no module is explicitly configured", async () => {
    vi.stubEnv("GOERP_MODULE_FRONTEND_NAME", "");
    vi.stubEnv("GOERP_MODULE_FRONTEND_ENTRY", "");
    expect(await source("serve")).toContain("developmentModuleName = null");
  });

  it("rejects an entry outside the local proxy", () => {
    vi.stubEnv("GOERP_MODULE_FRONTEND_NAME", "demo");
    vi.stubEnv("GOERP_MODULE_FRONTEND_ENTRY", "https://external.test/module.js");
    expect(() => source("serve")).toThrow("local module proxy");
  });
});
