import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import {
  noteTenantSuspension,
  TenantSuspensionStore,
  tenantSuspension,
  useTenantSuspended,
} from "./tenant-suspension.js";

describe("TenantSuspensionStore", () => {
  it("notifies subscribers only when the flag changes", () => {
    const store = new TenantSuspensionStore();
    const listener = vi.fn();
    store.subscribe(listener);

    store.set(true);
    store.set(true);
    store.set(false);

    expect(listener).toHaveBeenCalledTimes(2);
    expect(store.get()).toBe(false);
  });
});

describe("noteTenantSuspension", () => {
  it("flags only a 403 tenant_suspended", () => {
    noteTenantSuspension(new AppError({ code: "tenant_suspended", message: "", httpStatus: 404 }));
    noteTenantSuspension(new AppError({ code: "permission_denied", message: "", httpStatus: 403 }));
    expect(tenantSuspension.get()).toBe(false);

    noteTenantSuspension(new AppError({ code: "tenant_suspended", message: "", httpStatus: 403 }));
    expect(tenantSuspension.get()).toBe(true);
    tenantSuspension.set(false);
  });
});

describe("useTenantSuspended", () => {
  it("follows the shared store", () => {
    const { result } = renderHook(() => useTenantSuspended());
    expect(result.current).toBe(false);

    act(() => tenantSuspension.set(true));
    expect(result.current).toBe(true);

    act(() => tenantSuspension.set(false));
    expect(result.current).toBe(false);
  });
});
