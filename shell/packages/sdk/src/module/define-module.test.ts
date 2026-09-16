import { lazy } from "react";
import { describe, expect, it } from "vitest";
import { componentRegistry } from "../schema/index.js";
import { defineModule } from "./define-module.js";
import { extensionBatchLoaderRegistry } from "./extension-batch-loader-registry.js";
import { moduleErrorHandlerRegistry } from "./module-error-handler-registry.js";
import { moduleNavigationRegistry } from "./module-navigation-registry.js";

function lazyStub() {
  return lazy(() => Promise.resolve({ default: () => null }));
}

describe("defineModule", () => {
  it("registers views into ComponentRegistry, unnamespaced", () => {
    const view = lazyStub();
    defineModule({ name: "define_module_test_a", views: { DefineModuleTestView: view } });

    expect(componentRegistry.resolve("DefineModuleTestView")).toBe(view);
  });

  it("registers fieldRenderers into the same ComponentRegistry, keyed as given", () => {
    const renderer = lazyStub();
    defineModule({
      name: "define_module_test_b",
      fieldRenderers: { "define_module_test_b:custom_field": renderer },
    });

    expect(componentRegistry.resolve("define_module_test_b:custom_field")).toBe(renderer);
  });

  it("registers batchLoaders into ExtensionBatchLoaderRegistry", () => {
    const loader = async (ids: string[]) => new Map(ids.map((id) => [id, { field: id }]));
    defineModule({ name: "define_module_test_c", batchLoaders: { "sales.orders_list_test_c": loader } });

    expect(extensionBatchLoaderRegistry.resolve("sales.orders_list_test_c")).toBe(loader);
  });

  it("registers navigation into ModuleNavigationRegistry, keyed by module name", () => {
    const navigation = () => null;
    defineModule({ name: "define_module_test_d", navigation });

    expect(moduleNavigationRegistry.resolve("define_module_test_d")).toBe(navigation);
  });

  it("does not register into ModuleNavigationRegistry when navigation is omitted", () => {
    defineModule({ name: "define_module_test_e" });
    expect(moduleNavigationRegistry.resolve("define_module_test_e")).toBeUndefined();
  });

  it("registers errorHandlers into ModuleErrorHandlerRegistry, keyed by module name", () => {
    const handler = () => {};
    defineModule({ name: "define_module_test_f", errorHandlers: { "*": handler } });

    expect(moduleErrorHandlerRegistry.resolve("define_module_test_f", "anything")).toBe(handler);
  });

  it("re-registering the same module name (hot reload) replaces its views/fieldRenderers/batchLoaders instead of throwing", () => {
    const firstView = lazyStub();
    defineModule({
      name: "define_module_test_h",
      views: { DefineModuleTestHotReloadView: firstView },
      fieldRenderers: { "define_module_test_h:field": lazyStub() },
      batchLoaders: { "sales.orders_list_test_h": async (ids: string[]) => new Map(ids.map((id) => [id, {}])) },
    });

    const secondView = lazyStub();
    expect(() =>
      defineModule({
        name: "define_module_test_h",
        views: { DefineModuleTestHotReloadView: secondView },
      }),
    ).not.toThrow();

    expect(componentRegistry.resolve("DefineModuleTestHotReloadView")).toBe(secondView);
    // The old bundle's fieldRenderer/batchLoader, absent from the new
    // definition, are unregistered entirely rather than left stale.
    expect(componentRegistry.has("define_module_test_h:field")).toBe(false);
    expect(extensionBatchLoaderRegistry.has("sales.orders_list_test_h")).toBe(false);
  });

  it("clears a module's prior navigation/errorHandlers when a hot-reloaded definition omits them", () => {
    defineModule({
      name: "define_module_test_i",
      navigation: () => null,
      errorHandlers: { "*": () => {} },
    });
    expect(moduleNavigationRegistry.resolve("define_module_test_i")).toBeDefined();
    expect(moduleErrorHandlerRegistry.resolve("define_module_test_i", "anything")).toBeDefined();

    defineModule({ name: "define_module_test_i" });

    expect(moduleNavigationRegistry.resolve("define_module_test_i")).toBeUndefined();
    expect(moduleErrorHandlerRegistry.resolve("define_module_test_i", "anything")).toBeUndefined();
  });

  it("leaves commands and dispose untouched on the returned definition, for the app's own registration step", () => {
    const commands = [{ id: "x", label: "X", action: () => {} }];
    const dispose = () => {};
    const definition = defineModule({ name: "define_module_test_g", commands, dispose });

    expect(definition.commands).toBe(commands);
    expect(definition.dispose).toBe(dispose);
  });
});
