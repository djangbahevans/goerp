import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { AuthContext } from "./auth-provider.js";
import type { AuthContextValue, CurrentTenant, CurrentUser } from "./types.js";
import { useTenant } from "./use-tenant.js";

const USER: CurrentUser = {
  id: "u1",
  email: "a@b.com",
  name: null,
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const TENANT: CurrentTenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    state: { status: "authenticated", user: USER, tenant: TENANT },
    isAuthenticated: true,
    user: USER,
    tenant: TENANT,
    login: vi.fn(),
    logout: vi.fn(),
    submitMFA: vi.fn(),
    updateProfile: vi.fn(),
    updatePreferences: vi.fn(),
    changePassword: vi.fn(),
    reloadSession: vi.fn(),
    ...overrides,
  };
}

describe("useTenant", () => {
  it("returns the current tenant when authenticated", () => {
    const { result } = renderHook(() => useTenant(), {
      wrapper: ({ children }: { children: ReactNode }) => (
        <AuthContext.Provider value={authValue()}>{children}</AuthContext.Provider>
      ),
    });
    expect(result.current).toBe(TENANT);
  });

  it("throws when no tenant is authenticated", () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <AuthContext.Provider value={authValue({ tenant: null, isAuthenticated: false })}>
        {children}
      </AuthContext.Provider>
    );
    expect(() => renderHook(() => useTenant(), { wrapper })).toThrow(/must be called within an authenticated route/);
  });
});
