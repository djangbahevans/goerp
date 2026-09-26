import type { TenantSelection } from "@goerp/sdk/auth";

// The tenant_required answer a shared-domain sign-in hands to the tenant
// selector page. Memory only: the selection_token expires within two
// minutes, so a reload starts the sign-in over.
let pending: TenantSelection | null = null;

export const pendingTenantSelection = {
  get: (): TenantSelection | null => pending,
  set: (selection: TenantSelection | null): void => {
    pending = selection;
  },
};
