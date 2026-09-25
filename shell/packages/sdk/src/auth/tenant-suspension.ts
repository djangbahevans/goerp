import { useSyncExternalStore } from "react";
import type { AppError } from "../error/app-error.js";

type Listener = () => void;

// Set by any 403 tenant_suspended response (multitenancy-internals.md
// "Suspension"), so code outside the router, like the API client, can hand
// the shell's route gate the shell-ux.md §6.6 page. Memory only: after a
// reload, the session check or tenant-context lookup sets it again.
export class TenantSuspensionStore {
  private suspended = false;
  private readonly listeners = new Set<Listener>();

  get = (): boolean => this.suspended;

  set(suspended: boolean): void {
    if (suspended === this.suspended) return;
    this.suspended = suspended;
    for (const listener of this.listeners) listener();
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
}

export const tenantSuspension = new TenantSuspensionStore();

export function noteTenantSuspension(error: AppError): void {
  if (error.httpStatus === 403 && error.code === "tenant_suspended") tenantSuspension.set(true);
}

export function useTenantSuspended(): boolean {
  return useSyncExternalStore(tenantSuspension.subscribe, tenantSuspension.get, () => false);
}
