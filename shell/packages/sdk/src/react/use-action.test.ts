import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { toast } from "../notifications/toast.js";
import { ActionRegistry } from "./action-registry.js";
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
});

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
    const client = { post: vi.fn(async () => ({ id: "order-1", state: "confirmed" })) };
    const queryClient = new QueryClient();

    const opts = createActionMutationOptions(
      "sales.confirmOrder",
      {},
      queryClient,
      registry,
      client as never,
    );
    const result = await opts.mutationFn!("order-1", undefined as never);

    expect(client.post).toHaveBeenCalledWith("/orders/order-1/confirm", undefined);
    expect(result).toEqual({ id: "order-1", state: "confirmed" });
  });

  it("dispatches GET/PUT/PATCH/DELETE through the matching client method", async () => {
    const client = {
      get: vi.fn(async () => "get"),
      put: vi.fn(async () => "put"),
      patch: vi.fn(async () => "patch"),
      delete: vi.fn(async () => "delete"),
    };
    const queryClient = new QueryClient();

    for (const method of ["GET", "PUT", "PATCH", "DELETE"] as const) {
      const registry = fakeRegistry({ method, path: "/x" });
      const opts = createActionMutationOptions("m.a", {}, queryClient, registry, client as never);
      await opts.mutationFn!(undefined, undefined as never);
    }

    expect(client.get).toHaveBeenCalledTimes(1);
    expect(client.put).toHaveBeenCalledTimes(1);
    expect(client.patch).toHaveBeenCalledTimes(1);
    expect(client.delete).toHaveBeenCalledTimes(1);
  });

  it("forwards a GET action's body as query params instead of dropping it", async () => {
    const registry = fakeRegistry({ method: "GET", path: "/orders" });
    const client = { get: vi.fn(async () => []) };
    const queryClient = new QueryClient();

    const opts = createActionMutationOptions("sales.listOrders", {}, queryClient, registry, client as never);
    await opts.mutationFn!({ status: "open" }, undefined as never);

    expect(client.get).toHaveBeenCalledWith("/orders", { params: { status: "open" } });
  });

  it("throws instead of silently dropping a DELETE action's resolved body", async () => {
    const registry = fakeRegistry({ method: "DELETE", path: "/orders/{id}" });
    const client = { delete: vi.fn(async () => undefined) };
    const queryClient = new QueryClient();

    const opts = createActionMutationOptions("sales.deleteOrder", {}, queryClient, registry, client as never);

    await expect(opts.mutationFn!({ id: "o1", body: { reason: "x" } }, undefined as never)).rejects.toThrow(
      /cannot send one/,
    );
    expect(client.delete).not.toHaveBeenCalled();
  });

  it("invalidates every listed query key on success", async () => {
    const registry = fakeRegistry({ method: "POST", path: "/x" });
    const client = { post: vi.fn(async () => ({})) };
    const queryClient = new QueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");

    const opts = createActionMutationOptions(
      "sales.confirmOrder",
      { invalidates: [["sales", "orders"], ["sales", "dashboard"]] },
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

    expect(errorHandler).toHaveBeenCalledWith(err, { queryClient });
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
