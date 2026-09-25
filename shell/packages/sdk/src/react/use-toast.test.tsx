import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { toast, toastBus } from "../notifications/toast.js";
import { useToast } from "./use-toast.js";

afterEach(() => {
  cleanup();
  toastBus.dismiss();
});

describe("useToast", () => {
  it("returns the shell-wide toast instance, stable across renders", () => {
    const { result, rerender } = renderHook(() => useToast());
    const first = result.current;
    rerender();

    expect(result.current.toast).toBe(toast);
    expect(result.current).toBe(first);
  });

  it("resolves a loading toast in place with success", () => {
    const { result } = renderHook(() => useToast());
    const id = result.current.toast.loading("Generating invoice...");

    result.current.toast.success("Invoice generated", { id });

    expect(toastBus.getToasts()).toEqual([
      expect.objectContaining({ id, variant: "success", message: "Invoice generated" }),
    ]);
  });

  it("resolves a loading toast in place with error", () => {
    const { result } = renderHook(() => useToast());
    const id = result.current.toast.loading("Generating invoice...");

    result.current.toast.error("Failed to generate invoice", { id });

    expect(toastBus.getToasts()).toEqual([
      expect.objectContaining({ id, variant: "error", message: "Failed to generate invoice" }),
    ]);
  });
});
