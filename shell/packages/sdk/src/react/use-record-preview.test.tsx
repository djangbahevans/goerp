import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resourceRef } from "../names.js";
import type { ModelDef } from "../schema/index.js";
import { simulateRealtimeMessage } from "../testing/simulate-realtime-message.js";
import { recordQueryKey, useRecord } from "./use-record.js";

const { get, post, put, resolveModel, resolveResource } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  resolveModel: vi.fn(),
  resolveResource: vi.fn(),
}));

vi.mock("../schema/index.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../schema/index.js")>()),
  modelRegistry: { resolve: resolveModel },
  resourceRegistry: { resolve: resolveResource },
}));
vi.mock("../http/index.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../http/index.js")>()),
  apiClient: { get, post, put },
}));

type Order = Record<string, unknown> & {
  id: string;
  quantity: number;
  total: number;
  note: string;
  customer_id: string;
};
const resource = resourceRef<Order>("shop.order");
const initial: Order = { id: "one", quantity: 1, total: 10, note: "saved", customer_id: "customer-one" };

function model(): ModelDef {
  return {
    name: "order",
    label: "Order",
    label_plural: "Orders",
    shareable: false,
    enabled_ops: ["preview"],
    fields: [
      { name: "quantity", type: "integer" },
      { name: "customer_id", type: "many2one" },
      { name: "total", type: "integer", readonly: true, depends_on: ["quantity", "customer.discount"] },
    ],
  };
}

function mount(id: string | undefined, collaborationFields?: (keyof Order & string)[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  if (id) client.setQueryData(recordQueryKey(resource, id), initial);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const hook = renderHook(
    ({ recordId }) => useRecord(resource, recordId, collaborationFields === undefined ? {} : { collaborationFields }),
    {
      initialProps: { recordId: id },
      wrapper,
    },
  );
  return { ...hook, client };
}

async function advance(ms = 0) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe("useRecord live preview", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    resolveModel.mockResolvedValue(model());
    resolveResource.mockResolvedValue({
      getPath: "/orders/{id}",
      previewPath: "/orders/preview",
      updatePath: "/orders/{id}",
      updateMethod: "PUT",
    });
    get.mockResolvedValue(initial);
    post.mockResolvedValue({ ...initial, total: 20, note: "echoed draft" });
    put.mockResolvedValue({ ...initial, quantity: 2, total: 20 });
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.resetAllMocks();
  });

  it("debounces dependency edits and merges only computed fields into the displayed draft", async () => {
    const { result } = mount("one");
    await advance();
    expect(post).not.toHaveBeenCalled();
    act(() => result.current.setField("quantity", 2));
    await advance(200);
    act(() => result.current.setField("quantity", 3));
    await advance(299);
    expect(post).not.toHaveBeenCalled();
    await advance(1);
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith(
      "/orders/preview",
      { ...initial, quantity: 3 },
      { signal: expect.any(AbortSignal) },
    );
    expect(result.current.localRecord).toEqual({ ...initial, quantity: 3, total: 20 });
    expect(result.current.record).toEqual(initial);
    expect(result.current.unsavedEdits).toEqual({ quantity: 3 });
    expect(result.current.isDirty).toBe(true);
    expect(result.current.isPreviewing).toBe(false);
    await advance(1000);
    expect(post).toHaveBeenCalledTimes(1);
  });

  it("leaves a clean draft clean when preview changes its computed values", async () => {
    const { result } = mount("one");
    await advance();
    act(() => result.current.setField("quantity", 2));
    await advance(300);
    act(() => result.current.setField("quantity", 1));
    await advance(300);
    expect(result.current.localRecord?.total).toBe(20);
    expect(result.current.isDirty).toBe(false);
  });

  it("does not preview non-trigger edits", async () => {
    const { result } = mount("one");
    await advance();
    act(() => result.current.setField("note", "typed"));
    await advance(1000);
    expect(post).not.toHaveBeenCalled();
  });

  it("rejects a superseded response before the next debounce expires", async () => {
    let complete!: (value: Partial<Order>) => void;
    post.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = resolve;
        }),
    );
    const { result } = mount("one");
    await advance();
    act(() => result.current.setField("quantity", 2));
    await advance(300);
    act(() => result.current.setField("quantity", 3));
    await act(async () => {
      complete({ total: 999 });
    });
    expect(result.current.localRecord?.total).toBe(10);
    await advance(300);
    expect(result.current.localRecord?.total).toBe(20);
  });

  it("retains dependency edits made before schema metadata resolves", async () => {
    let complete!: (value: ModelDef) => void;
    resolveModel.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = resolve;
        }),
    );
    const { result } = mount("one");
    act(() => result.current.setField("quantity", 2));
    await act(async () => {
      complete(model());
    });
    await advance(300);
    expect(post).toHaveBeenCalledTimes(1);
    expect(result.current.localRecord?.total).toBe(20);
  });

  it("cancels a queued preview on discard", async () => {
    const { result } = mount("one");
    await advance();
    act(() => result.current.setField("quantity", 2));
    await advance(200);
    act(() => result.current.discard());
    await advance(1000);
    expect(post).not.toHaveBeenCalled();
    expect(result.current.localRecord).toEqual(initial);
  });

  it("maps dotted Many2One dependencies to their foreign-key edit", async () => {
    const { result } = mount("one");
    await advance();
    act(() => result.current.setField("customer_id", "customer-two"));
    await advance(300);
    expect(post).toHaveBeenCalledWith(
      "/orders/preview",
      { ...initial, customer_id: "customer-two" },
      expect.anything(),
    );
  });

  it.each(["disabled", "no dependencies", "no route"])("makes no calls for %s", async (kind) => {
    const declaration = model();
    if (kind === "disabled") declaration.enabled_ops = [];
    if (kind === "no dependencies") declaration.fields = [{ name: "quantity", type: "integer" }];
    if (kind === "no route") resolveResource.mockResolvedValue({ getPath: "/orders/{id}", previewPath: null });
    resolveModel.mockResolvedValue(declaration);
    const { result } = mount("one");
    await advance();
    act(() => result.current.setField("quantity", 2));
    await advance(1000);
    expect(post).not.toHaveBeenCalled();
  });

  it("previews create drafts without an id", async () => {
    const { result } = mount(undefined);
    await advance();
    act(() => result.current.setFields({ quantity: 2, note: "new" }));
    await advance(300);
    expect(post).toHaveBeenCalledWith("/orders/preview", { quantity: 2, note: "new" }, expect.anything());
    expect(result.current.localRecord).toEqual({ quantity: 2, note: "new", total: 20 });
  });

  it("exposes in-flight state and discards out-of-order responses, including non-trigger edits", async () => {
    let first!: (value: Partial<Order>) => void;
    let second!: (value: Partial<Order>) => void;
    post.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          first = resolve;
        }),
    );
    post.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          second = resolve;
        }),
    );
    const { result } = mount("one");
    await advance();
    act(() => result.current.setField("quantity", 2));
    expect(result.current.isPreviewing).toBe(false);
    await advance(300);
    expect(result.current.isPreviewing).toBe(true);
    act(() => result.current.setField("note", "newer draft"));
    await advance(300);
    await act(async () => {
      second({ total: 30 });
    });
    expect(result.current.localRecord?.total).toBe(30);
    await act(async () => {
      first({ total: 20 });
    });
    expect(result.current.localRecord?.total).toBe(30);
    expect(result.current.localRecord?.note).toBe("newer draft");
    expect(result.current.isPreviewing).toBe(false);
  });

  it.each([false, true])("conflicts on a computed server change only with explicit inclusion=%s", async (explicit) => {
    const { result, client } = mount("one", explicit ? ["total"] : undefined);
    await advance();
    act(() => result.current.setField("quantity", 2));
    await advance(300);
    act(() => client.setQueryData(recordQueryKey(resource, "one"), { ...initial, total: 30 }));
    await advance();
    expect(result.current.hasConflict).toBe(explicit);
  });

  it.each(["discard", "navigate", "unavailable", "unmount", "save"])("ignores a response after %s", async (action) => {
    let complete!: (value: Partial<Order>) => void;
    post.mockImplementation(
      () =>
        new Promise((resolve) => {
          complete = resolve;
        }),
    );
    const { result, rerender, unmount } = mount("one");
    await advance();
    act(() => result.current.setField("quantity", 2));
    await advance(300);
    const signal = post.mock.calls[0]?.[2].signal as AbortSignal;
    if (action === "discard") act(() => result.current.discard());
    if (action === "navigate") act(() => rerender({ recordId: "two" }));
    if (action === "unavailable")
      act(() =>
        simulateRealtimeMessage(`record:${resource}:one`, { type: "record.unavailable", resource, recordId: "one" }),
      );
    if (action === "unmount") unmount();
    if (action === "save")
      await act(async () => {
        await result.current.save();
      });
    expect(signal.aborted).toBe(true);
    await act(async () => {
      complete({ total: 999 });
    });
    if (action !== "unmount") {
      expect(result.current.localRecord?.total).not.toBe(999);
      expect(result.current.isPreviewing).toBe(false);
    }
    if (action === "save") expect(put).toHaveBeenCalledWith("/orders/one", { quantity: 2 }, expect.anything());
  });

  it("recovers from a preview failure on the next dependency edit", async () => {
    post.mockRejectedValueOnce(new Error("invalid draft"));
    const { result } = mount("one");
    await advance();
    act(() => result.current.setField("quantity", 2));
    await advance(300);
    expect(result.current.isPreviewing).toBe(false);
    expect(result.current.isError).toBe(false);
    act(() => result.current.setField("quantity", 3));
    await advance(300);
    expect(result.current.localRecord?.total).toBe(20);
  });
});
