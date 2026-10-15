import { MutationObserver, QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { moduleErrorHandlerRegistry } from "../module/module-error-handler-registry.js";
import { toast } from "../notifications/toast.js";
import type { ActionRegistry } from "./action-registry.js";
import { createActionMutationOptions, splitPathAndBody } from "./use-action.js";

describe("splitPathAndBody", () => {
  it("sends variables as the body unchanged when the path has no placeholder", () => {
    expect(splitPathAndBody("/orders/bulk_import", { file: "x" })).toEqual({
      path: "/orders/bulk_import",
      body: { file: "x" },
    });
  });

  it("fills the placeholder from a bare scalar and sends no body", () => {
    expect(splitPathAndBody("/orders/{id}/confirm", "order-1")).toEqual({
      path: "/orders/order-1/confirm",
      body: undefined,
    });
  });

  it("fills the placeholder from a matching object key and uses its body field", () => {
    expect(splitPathAndBody("/contacts/{id}", { id: "c1", body: { name: "New Name" } })).toEqual({
      path: "/contacts/c1",
      body: { name: "New Name" },
    });
  });

  it("falls back to the object's remaining keys when there's no body field", () => {
    expect(splitPathAndBody("/contacts/{id}", { id: "c1", name: "New Name" })).toEqual({
      path: "/contacts/c1",
      body: { name: "New Name" },
    });
  });

  it("sends no body when the object has only the placeholder key", () => {
    expect(splitPathAndBody("/orders/{id}/confirm", { id: "order-1" })).toEqual({
      path: "/orders/order-1/confirm",
      body: undefined,
    });
  });

  it("throws a clear error when an object variable doesn't carry the placeholder key", () => {
    expect(() => splitPathAndBody("/orders/{id}/confirm", { foo: "bar" })).toThrow(/needs a "id" variable/);
  });

  it('throws a clear error instead of substituting the literal string "undefined" into a placeholder', () => {
    expect(() => splitPathAndBody("/orders/{id}/confirm", undefined)).toThrow(/needs a "id" variable/);
  });
});

function fakeClient(body: unknown = {}, headers: HeadersInit = {}) {
  return { request: vi.fn(async () => ({ body, headers: new Headers(headers) })) };
}

function fakeRegistry(route: { method: string; path: string }): ActionRegistry {
  return { resolve: vi.fn(async () => route) } as unknown as ActionRegistry;
}

afterEach(() => {
  vi.restoreAllMocks();
  toast.dismiss();
});

describe("createActionMutationOptions", () => {
  it("mutationFn resolves the route and dispatches through the given client", async () => {
    const registry = fakeRegistry({ method: "POST", path: "/orders/{id}/confirm" });
    const client = fakeClient({ id: "order-1", state: "confirmed" });

    const opts = createActionMutationOptions("sales.confirmOrder", {}, new QueryClient(), registry, client as never);
    const result = await opts.mutationFn!("order-1", undefined as never);

    expect(client.request).toHaveBeenCalledWith("POST", "/orders/order-1/confirm", undefined);
    expect(result).toEqual({ id: "order-1", state: "confirmed" });
  });

  it("forwards a GET action's body as query params instead of dropping it", async () => {
    const registry = fakeRegistry({ method: "GET", path: "/orders" });
    const client = fakeClient([]);

    const opts = createActionMutationOptions("sales.listOrders", {}, new QueryClient(), registry, client as never);
    await opts.mutationFn!({ status: "open" }, undefined as never);

    expect(client.request).toHaveBeenCalledWith("GET", "/orders", undefined, { params: { status: "open" } });
  });

  it("sends a DELETE action's resolved body", async () => {
    const registry = fakeRegistry({ method: "DELETE", path: "/orders/{id}" });
    const client = fakeClient();

    const opts = createActionMutationOptions("sales.deleteOrder", {}, new QueryClient(), registry, client as never);
    await opts.mutationFn!({ id: "o1", body: { reason: "x" } }, undefined as never);

    expect(client.request).toHaveBeenCalledWith("DELETE", "/orders/o1", { reason: "x" });
  });

  it("runs an envelope's instructions before onSuccess, invalidates and successMessage, with its data", async () => {
    const order: string[] = [];
    vi.spyOn(toast, "info").mockImplementation((message) => {
      order.push(`toast:${message}`);
    });
    vi.spyOn(toast, "success").mockImplementation((message) => {
      order.push(`successMessage:${message}`);
    });
    const queryClient = new QueryClient();
    vi.spyOn(queryClient, "invalidateQueries").mockImplementation(async () => {
      order.push("invalidates");
    });
    const registry = fakeRegistry({ method: "POST", path: "/orders/{id}/confirm" });
    const client = fakeClient(
      { data: { id: "o1" }, ui: [{ type: "toast", message: "Confirmed", severity: "info" }] },
      { "X-GoERP-UI": "1" },
    );

    const observer = new MutationObserver(
      queryClient,
      createActionMutationOptions<unknown, string>(
        "sales.confirmOrder",
        {
          invalidates: [["orders"]],
          successMessage: "Saved",
          onSuccess: (data) => order.push(`onSuccess:${JSON.stringify(data)}`),
        },
        queryClient,
        registry,
        client as never,
      ),
    );
    await expect(observer.mutate("o1")).resolves.toEqual({ id: "o1" });

    expect(order).toEqual(["toast:Confirmed", 'onSuccess:{"id":"o1"}', "successMessage:Saved", "invalidates"]);
  });

  it("invalidates every listed query key on success", async () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const client = fakeClient();
    const queryClient = new QueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");

    const opts = createActionMutationOptions(
      "sales.confirmOrder",
      {
        invalidates: [
          ["sales", "orders"],
          ["sales", "dashboard"],
        ],
      },
      queryClient,
      registry,
      client as never,
    );
    opts.onSuccess!({}, undefined as never, undefined, undefined as never);

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["sales", "orders"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["sales", "dashboard"] });
  });

  it("calls onError with the structured AppError", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const onError = vi.fn();
    const err = new AppError({ code: "sales.order.insufficient_stock", message: "no stock", httpStatus: 422 });

    const opts = createActionMutationOptions("sales.confirmOrder", { onError }, queryClient, registry, {} as never);
    opts.onError!(err, undefined as never, undefined, undefined as never);

    expect(onError).toHaveBeenCalledWith(err, undefined, undefined);
  });

  it("calls errorHandler instead of/in addition to onError when provided", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const errorHandler = vi.fn();
    const err = new AppError({ code: "x", message: "x", httpStatus: 500 });

    const opts = createActionMutationOptions(
      "sales.confirmOrder",
      { errorHandler },
      queryClient,
      registry,
      {} as never,
    );
    opts.onError!(err, undefined as never, undefined, undefined as never);

    expect(errorHandler).toHaveBeenCalledWith(err, { queryClient, toast });
  });

  it("falls back to the route's module's registered error handler when no local onError/errorHandler is given", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const moduleHandler = vi.fn();
    moduleErrorHandlerRegistry.register("sales", { "sales.order.insufficient_stock": moduleHandler });
    const err = new AppError({ code: "sales.order.insufficient_stock", message: "no stock", httpStatus: 422 });

    const opts = createActionMutationOptions("sales.confirmOrder", {}, queryClient, registry, {} as never);
    opts.onError!(err, undefined as never, undefined, undefined as never);

    expect(moduleHandler).toHaveBeenCalledWith(err, { queryClient, toast });
  });

  it("falls back to the module's '*' wildcard when no exact-code handler is registered", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const wildcardHandler = vi.fn();
    moduleErrorHandlerRegistry.register("sales", { "*": wildcardHandler });
    const err = new AppError({ code: "sales.order.unknown_thing", message: "boom", httpStatus: 500 });

    const opts = createActionMutationOptions("sales.confirmOrder", {}, queryClient, registry, {} as never);
    opts.onError!(err, undefined as never, undefined, undefined as never);

    expect(wildcardHandler).toHaveBeenCalledWith(err, { queryClient, toast });
  });

  it("skips the module's registered default entirely when a local onError is given", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const moduleHandler = vi.fn();
    moduleErrorHandlerRegistry.register("sales", { "*": moduleHandler });
    const onError = vi.fn();
    const err = new AppError({ code: "sales.order.unknown_thing", message: "boom", httpStatus: 500 });

    const opts = createActionMutationOptions("sales.confirmOrder", { onError }, queryClient, registry, {} as never);
    opts.onError!(err, undefined as never, undefined, undefined as never);

    expect(moduleHandler).not.toHaveBeenCalled();
    expect(onError).toHaveBeenCalledWith(err, undefined, undefined);
  });

  it("does not fall back to a module default when no module is registered for the route", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const err = new AppError({ code: "unregistered_module.thing", message: "boom", httpStatus: 500 });

    const opts = createActionMutationOptions("unregistered_module.doThing", {}, queryClient, registry, {} as never);

    expect(() => opts.onError!(err, undefined as never, undefined, undefined as never)).not.toThrow();
  });

  it("does not call errorHandler when it is null", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const err = new AppError({ code: "x", message: "x", httpStatus: 500 });

    const opts = createActionMutationOptions(
      "sales.confirmOrder",
      { errorHandler: null },
      queryClient,
      registry,
      {} as never,
    );
    expect(() => opts.onError!(err, undefined as never, undefined, undefined as never)).not.toThrow();
  });

  it("triggers a toast when successMessage is set, string or function", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const toastSpy = vi.spyOn(toast, "success");

    const withString = createActionMutationOptions(
      "sales.confirmOrder",
      { successMessage: "Order confirmed" },
      queryClient,
      registry,
      {} as never,
    );
    withString.onSuccess!({}, undefined as never, undefined, undefined as never);
    expect(toastSpy).toHaveBeenCalledWith("Order confirmed");

    const withFn = createActionMutationOptions(
      "sales.confirmOrder",
      { successMessage: (data: { reference: string }) => `Order ${data.reference} confirmed` },
      queryClient,
      registry,
      {} as never,
    );
    withFn.onSuccess!({ reference: "SO-1" }, undefined as never, undefined, undefined as never);
    expect(toastSpy).toHaveBeenCalledWith("Order SO-1 confirmed");
  });

  it("triggers no toast when successMessage is omitted", () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const queryClient = new QueryClient();
    const toastSpy = vi.spyOn(toast, "success");

    const opts = createActionMutationOptions("sales.confirmOrder", {}, queryClient, registry, {} as never);
    opts.onSuccess!({}, undefined as never, undefined, undefined as never);

    expect(toastSpy).not.toHaveBeenCalled();
  });
});
