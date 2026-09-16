import { describe, expect, it } from "vitest";
import { ModuleErrorHandlerRegistry } from "./module-error-handler-registry.js";

describe("ModuleErrorHandlerRegistry", () => {
  it("resolves a module's exact-code handler", () => {
    const registry = new ModuleErrorHandlerRegistry();
    const handler = () => {};
    registry.register("contacts", { "contacts.contact.duplicate_email": handler });
    expect(registry.resolve("contacts", "contacts.contact.duplicate_email")).toBe(handler);
  });

  it("falls back to the module's '*' wildcard when no exact code matches", () => {
    const registry = new ModuleErrorHandlerRegistry();
    const wildcard = () => {};
    registry.register("contacts", { "*": wildcard });
    expect(registry.resolve("contacts", "contacts.contact.unknown_thing")).toBe(wildcard);
  });

  it("prefers the exact code over the wildcard when both are registered", () => {
    const registry = new ModuleErrorHandlerRegistry();
    const exact = () => {};
    const wildcard = () => {};
    registry.register("contacts", { "contacts.contact.duplicate_email": exact, "*": wildcard });
    expect(registry.resolve("contacts", "contacts.contact.duplicate_email")).toBe(exact);
  });

  it("returns undefined for an unregistered module", () => {
    const registry = new ModuleErrorHandlerRegistry();
    expect(registry.resolve("missing", "any.code")).toBeUndefined();
  });

  it("returns undefined when the module has no matching code or wildcard", () => {
    const registry = new ModuleErrorHandlerRegistry();
    registry.register("contacts", { "contacts.contact.duplicate_email": () => {} });
    expect(registry.resolve("contacts", "contacts.contact.other")).toBeUndefined();
  });

  it("replaces a module's own prior registration rather than throwing (hot-reload re-registration)", () => {
    const registry = new ModuleErrorHandlerRegistry();
    registry.register("contacts", { "*": () => {} });
    const replacement = () => {};
    expect(() => registry.register("contacts", { "*": replacement })).not.toThrow();
    expect(registry.resolve("contacts", "anything")).toBe(replacement);
  });
});
