import { QueryClient } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "../notifications/toast.js";
import { type ExecutionContext, useExecutionContext } from "./execution-context.js";
import { modalManager } from "./modal-manager.js";
import { configureUIExecutor, executeInstructions, resolveActionResponse, type UIInstruction } from "./ui-executor.js";

function View({ ctx }: { ctx: ExecutionContext }) {
  useExecutionContext(ctx);
  return null;
}

const invoiceForm: ExecutionContext = {
  view: "form",
  resource: "sales.invoice",
  recordId: "01j",
  record: { id: "01j", customer_id: "c7" },
  queryKey: ["record", "sales.invoice", "01j"],
};

const invoiceList: ExecutionContext = {
  view: "list",
  resource: "sales.invoice",
  recordId: null,
  record: null,
  queryKey: ["infinite-list", null, "sales.invoice"],
};

let queryClient: QueryClient;
let navigate: ReturnType<typeof vi.fn<(href: string) => void>>;
let unmountView: (() => void) | undefined;

function mountView(ctx: ExecutionContext) {
  unmountView = render(<View ctx={ctx} />).unmount;
}

beforeEach(() => {
  queryClient = new QueryClient();
  navigate = vi.fn<(href: string) => void>();
  configureUIExecutor({ queryClient, navigate });
});

afterEach(() => {
  unmountView?.();
  unmountView = undefined;
  configureUIExecutor(null);
  while (modalManager.getStack().length > 0) modalManager.closeTop();
  toast.dismiss();
  vi.restoreAllMocks();
});

describe("executeInstructions", () => {
  it("runs instructions in order, each finishing before the next starts", async () => {
    mountView(invoiceForm);
    const order: string[] = [];
    let finishReload: () => void = () => {};
    vi.spyOn(queryClient, "invalidateQueries").mockImplementation(() => {
      order.push("reload started");
      return new Promise<void>((resolve) => {
        finishReload = () => {
          order.push("reload finished");
          resolve();
        };
      });
    });
    vi.spyOn(toast, "success").mockImplementation((message) => {
      order.push(`toast ${message}`);
    });

    const done = executeInstructions(
      [{ type: "reload" }, { type: "toast", message: "Saved", severity: "success" }],
      "response",
    );
    await vi.waitFor(() => expect(order).toEqual(["reload started"]));
    finishReload();
    await done;

    expect(order).toEqual(["reload started", "reload finished", "toast Saved"]);
  });

  it("shows a toast with its severity and duration, defaulting to 4 seconds", async () => {
    const warning = vi.spyOn(toast, "warning");
    const info = vi.spyOn(toast, "info");

    await executeInstructions(
      [
        { type: "toast", message: "Check stock", severity: "warning", duration_ms: 0 },
        { type: "toast", message: "Heads up", severity: "nonsense" as never },
      ],
      "response",
    );

    expect(warning).toHaveBeenCalledWith("Check stock", { duration: 0 });
    expect(info).toHaveBeenCalledWith("Heads up", { duration: 4000 });
  });

  describe("reload", () => {
    it.each<[string, ExecutionContext, Extract<UIInstruction, { type: "reload" }>, boolean]>([
      ["untargeted, on a form", invoiceForm, { type: "reload" }, true],
      ["untargeted, on a list", invoiceList, { type: "reload" }, true],
      [
        "a record target, on that record's form",
        invoiceForm,
        { type: "reload", resource: "sales.invoice", record_id: "01j" },
        true,
      ],
      [
        "a record target, on another record's form",
        invoiceForm,
        { type: "reload", resource: "sales.invoice", record_id: "02k" },
        false,
      ],
      [
        "a record target, on the resource's list",
        invoiceList,
        { type: "reload", resource: "sales.invoice", record_id: "01j" },
        false,
      ],
      ["a list target, on that resource's list", invoiceList, { type: "reload", resource: "sales.invoice" }, true],
      ["a list target, on another resource's list", invoiceList, { type: "reload", resource: "sales.order" }, false],
      ["a list target, on a form of that resource", invoiceForm, { type: "reload", resource: "sales.invoice" }, false],
    ])("%s: reloads=%s", async (_, ctx, instruction, reloads) => {
      mountView(ctx);
      const invalidate = vi.spyOn(queryClient, "invalidateQueries");

      await executeInstructions([instruction], "response");

      if (reloads) expect(invalidate).toHaveBeenCalledWith({ queryKey: ctx.queryKey });
      else expect(invalidate).not.toHaveBeenCalled();
    });

    it("is a no-op untargeted on a page with no registered view", async () => {
      const invalidate = vi.spyOn(queryClient, "invalidateQueries");
      await executeInstructions([{ type: "reload" }], "response");
      expect(invalidate).not.toHaveBeenCalled();
    });
  });

  it("set_badge writes the count into the badge's cached poll result", async () => {
    await executeInstructions([{ type: "set_badge", target: "/hr/leave/approvals/count", count: 4 }], "response");
    expect(queryClient.getQueryData(["nav-badge", "/hr/leave/approvals/count"])).toEqual({ count: 4 });
  });

  describe("navigate", () => {
    it("interpolates from the current form's record and navigates to the module path", async () => {
      mountView(invoiceForm);
      await executeInstructions([{ type: "navigate", path: "/contacts/{record.customer_id}/invoices" }], "response");
      expect(navigate).toHaveBeenCalledWith("/_m/contacts/c7/invoices");
    });

    it("drops a segment whose field is missing, as interpolatePath does", async () => {
      vi.spyOn(console, "warn").mockImplementation(() => {});
      mountView(invoiceList);
      await executeInstructions([{ type: "navigate", path: "/sales/invoices/{record.id}" }], "response");
      expect(navigate).toHaveBeenCalledWith("/_m/sales/invoices");
    });
  });

  it("reads the execution context when each instruction starts", async () => {
    mountView(invoiceList);
    navigate.mockImplementation(() => {
      unmountView?.();
      mountView(invoiceForm);
    });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    await executeInstructions(
      [
        { type: "navigate", path: "/sales/invoices/01j" },
        { type: "reload", resource: "sales.invoice", record_id: "01j" },
      ],
      "response",
    );

    expect(invalidate).toHaveBeenCalledWith({ queryKey: invoiceForm.queryKey });
  });

  describe("open_url", () => {
    it("opens a new tab by default", async () => {
      const open = vi.spyOn(window, "open").mockReturnValue(null);
      await executeInstructions([{ type: "open_url", url: "https://checkout.paystack.com/abc" }], "response");
      expect(open).toHaveBeenCalledWith("https://checkout.paystack.com/abc", "_blank", "noopener,noreferrer");
    });

    it('opens in the current tab with target "self"', async () => {
      const assign = vi.fn();
      vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign });
      const open = vi.spyOn(window, "open");

      await executeInstructions(
        [{ type: "open_url", url: "https://checkout.paystack.com/abc", target: "self" }],
        "response",
      );

      expect(assign).toHaveBeenCalledWith("https://checkout.paystack.com/abc");
      expect(open).not.toHaveBeenCalled();
    });

    it("skips a URL that isn't http(s)", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const open = vi.spyOn(window, "open");
      await executeInstructions([{ type: "open_url", url: "javascript:alert(1)" }], "response");
      expect(open).not.toHaveBeenCalled();
      expect(warn).toHaveBeenCalledOnce();
    });
  });

  describe("close_modal", () => {
    it("closes the topmost modal", async () => {
      modalManager.open({ resource: "sales.invoice" });
      modalManager.open({ resource: "sales.order" });
      await executeInstructions([{ type: "close_modal" }], "response");
      expect(modalManager.getStack()).toEqual([{ kind: "form", config: { resource: "sales.invoice" } }]);
    });

    it("is a no-op with an empty stack", async () => {
      const success = vi.spyOn(toast, "success");
      await executeInstructions(
        [{ type: "close_modal" }, { type: "toast", message: "Done", severity: "success" }],
        "response",
      );
      expect(modalManager.getStack()).toEqual([]);
      expect(success).toHaveBeenCalledOnce();
    });
  });

  it("skips open_form, open_dialog and unknown types with a warning and runs the rest", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const success = vi.spyOn(toast, "success");

    await executeInstructions(
      [
        { type: "open_form", resource: "sales.invoice" },
        { type: "open_dialog", title: "Pay", action: { method: "POST", path: "/pay" }, fields: [] },
        { type: "explode" } as never,
        { type: "toast", message: "Done", severity: "success" },
      ],
      "response",
    );

    expect(warn).toHaveBeenCalledTimes(3);
    expect(modalManager.getStack()).toEqual([]);
    expect(success).toHaveBeenCalledOnce();
  });

  it("logs a failing instruction and runs the rest", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    navigate.mockImplementation(() => {
      throw new Error("router not ready");
    });
    const success = vi.spyOn(toast, "success");

    await executeInstructions(
      [
        { type: "navigate", path: "/sales" },
        { type: "toast", message: "Done", severity: "success" },
      ],
      "response",
    );

    expect(error).toHaveBeenCalledOnce();
    expect(success).toHaveBeenCalledOnce();
  });

  it("runs only toast, reload and set_badge from a push", async () => {
    mountView(invoiceForm);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const info = vi.spyOn(toast, "info");
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    modalManager.open({ resource: "sales.invoice" });

    await executeInstructions(
      [
        { type: "navigate", path: "/sales" },
        { type: "open_form", resource: "sales.invoice" },
        { type: "open_dialog", title: "Pay", action: { method: "POST", path: "/pay" }, fields: [] },
        { type: "close_modal" },
        { type: "open_url", url: "https://example.com" },
        { type: "toast", message: "Paid", severity: "info" },
        { type: "reload" },
        { type: "set_badge", target: "/count", count: 2 },
      ],
      "push",
    );

    expect(navigate).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
    expect(modalManager.getStack()).toHaveLength(1);
    expect(warn).toHaveBeenCalledTimes(5);
    expect(info).toHaveBeenCalledWith("Paid", { duration: 4000 });
    expect(invalidate).toHaveBeenCalledOnce();
    expect(queryClient.getQueryData(["nav-badge", "/count"])).toEqual({ count: 2 });
  });

  it("skips host-dependent instructions with a warning before the shell configures the executor", async () => {
    configureUIExecutor(null);
    mountView(invoiceForm);
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

    await executeInstructions(
      [{ type: "reload" }, { type: "set_badge", target: "/count", count: 1 }, { type: "navigate", path: "/x" }],
      "response",
    );

    expect(warn).toHaveBeenCalledTimes(3);
    expect(navigate).not.toHaveBeenCalled();
  });
});

describe("resolveActionResponse", () => {
  const envelopeHeaders = new Headers({ "X-GoERP-UI": "1" });

  it("runs an envelope's instructions and resolves with its data", async () => {
    const success = vi.spyOn(toast, "success");
    const data = await resolveActionResponse("POST", {
      body: { data: { id: "o1" }, ui: [{ type: "toast", message: "Confirmed", severity: "success" }] },
      headers: envelopeHeaders,
    });
    expect(data).toEqual({ id: "o1" });
    expect(success).toHaveBeenCalledWith("Confirmed", { duration: 4000 });
  });

  it("resolves an envelope without data to undefined", async () => {
    await expect(
      resolveActionResponse("POST", { body: { ui: [] }, headers: envelopeHeaders }),
    ).resolves.toBeUndefined();
  });

  it("returns a plain body with its own ui field unchanged and runs nothing", async () => {
    const success = vi.spyOn(toast, "success");
    const body = { data: { id: "o1" }, ui: [{ type: "toast", message: "Confirmed", severity: "success" }] };

    await expect(resolveActionResponse("POST", { body, headers: new Headers() })).resolves.toBe(body);
    expect(success).not.toHaveBeenCalled();
  });

  it("re-fetches the current view after a plain response to a write, but not to a read", async () => {
    mountView(invoiceForm);
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    await resolveActionResponse("GET", { body: {}, headers: new Headers() });
    expect(invalidate).not.toHaveBeenCalled();

    await resolveActionResponse("POST", { body: {}, headers: new Headers() });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: invoiceForm.queryKey });
  });

  it("doesn't re-fetch after an envelope; the handler sends reload when it wants one", async () => {
    mountView(invoiceForm);
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    await resolveActionResponse("POST", { body: { data: {}, ui: [] }, headers: envelopeHeaders });
    expect(invalidate).not.toHaveBeenCalled();
  });
});
