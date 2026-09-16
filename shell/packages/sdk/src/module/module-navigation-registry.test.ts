import { describe, expect, it } from "vitest";
import { ModuleNavigationRegistry } from "./module-navigation-registry.js";

describe("ModuleNavigationRegistry", () => {
  it("resolves a registered module's navigation function", () => {
    const registry = new ModuleNavigationRegistry();
    const navigation = () => null;
    registry.register("contacts", navigation);
    expect(registry.resolve("contacts")).toBe(navigation);
  });

  it("returns undefined for a module with no registered navigation", () => {
    const registry = new ModuleNavigationRegistry();
    expect(registry.resolve("missing")).toBeUndefined();
  });

  it("replaces a module's own prior registration rather than throwing (hot-reload re-registration)", () => {
    const registry = new ModuleNavigationRegistry();
    registry.register("contacts", () => null);
    const replacement = () => null;
    expect(() => registry.register("contacts", replacement)).not.toThrow();
    expect(registry.resolve("contacts")).toBe(replacement);
  });
});
