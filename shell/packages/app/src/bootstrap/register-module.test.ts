import type { ModuleDefinition } from "@goerp/sdk";
import { describe, expect, it, vi } from "vitest";
import { commandRegistry } from "../chrome/command-registry.js";
import { registerModule } from "./register-module.js";

describe("registerModule", () => {
  it("registers the definition's commands into the shell's CommandRegistry", () => {
    const definition: ModuleDefinition = {
      name: "register_module_test_a",
      commands: [{ id: "register_module_test_a.new", label: "New", action: vi.fn() }],
    };

    registerModule(definition);

    expect(commandRegistry.getAll().map((c) => c.id)).toContain("register_module_test_a.new");
  });

  it("returns an unregister function that removes exactly this module's commands", () => {
    const definition: ModuleDefinition = {
      name: "register_module_test_b",
      commands: [{ id: "register_module_test_b.new", label: "New", action: vi.fn() }],
    };

    const unregister = registerModule(definition);
    unregister();

    expect(commandRegistry.getAll().map((c) => c.id)).not.toContain("register_module_test_b.new");
  });

  it("no-ops without throwing when commands is omitted", () => {
    const definition: ModuleDefinition = { name: "register_module_test_c" };
    expect(() => registerModule(definition)).not.toThrow();
  });
});
