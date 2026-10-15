import { render, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { currentExecutionContext, type ExecutionContext, useExecutionContext } from "./execution-context.js";

function formContext(recordId: string, record: Record<string, unknown> = { id: recordId }): ExecutionContext {
  return { view: "form", resource: "sales.invoice", recordId, record, queryKey: ["record", "sales.invoice", recordId] };
}

const listContext: ExecutionContext = {
  view: "list",
  resource: "sales.invoice",
  recordId: null,
  record: null,
  queryKey: ["infinite-list", null, "sales.invoice"],
};

function View({ ctx }: { ctx: ExecutionContext | null }) {
  useExecutionContext(ctx);
  return null;
}

describe("useExecutionContext", () => {
  it("is empty with no view mounted", () => {
    expect(currentExecutionContext()).toEqual({
      view: null,
      resource: null,
      recordId: null,
      record: null,
      queryKey: null,
    });
  });

  it("follows navigation from one view to the next", () => {
    const { rerender, unmount } = render(<View key="list" ctx={listContext} />);
    expect(currentExecutionContext()).toBe(listContext);

    const form = formContext("01j");
    rerender(<View key="form" ctx={form} />);
    expect(currentExecutionContext()).toBe(form);

    unmount();
    expect(currentExecutionContext().view).toBeNull();
  });

  it("makes the most recently mounted of two views current, and the other current again once it unmounts", () => {
    const form = formContext("01j");
    const { rerender, unmount } = render(<View ctx={listContext} />);
    const modal = render(<View ctx={form} />);
    expect(currentExecutionContext()).toBe(form);

    modal.unmount();
    expect(currentExecutionContext()).toBe(listContext);

    // Re-rendering the page underneath must not move it above a later view.
    const second = render(<View ctx={form} />);
    rerender(<View ctx={{ ...listContext, record: { changed: true } }} />);
    expect(currentExecutionContext()).toBe(form);

    second.unmount();
    unmount();
  });

  it("reflects the latest render's record without remounting", () => {
    const { rerender, unmount } = renderHook(({ ctx }) => useExecutionContext(ctx), {
      initialProps: { ctx: formContext("01j", { id: "01j", state: "draft" }) },
    });
    expect(currentExecutionContext().record).toEqual({ id: "01j", state: "draft" });

    rerender({ ctx: formContext("01j", { id: "01j", state: "posted" }) });
    expect(currentExecutionContext().record).toEqual({ id: "01j", state: "posted" });
    unmount();
  });

  it("registers nothing for a null context", () => {
    const page = render(<View ctx={listContext} />);
    const embedded = render(<View ctx={null} />);

    expect(currentExecutionContext()).toBe(listContext);
    embedded.unmount();
    page.unmount();
  });
});
