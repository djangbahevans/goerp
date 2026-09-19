import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { AuthContext } from "./auth-provider.js";
import type { AuthContextValue, CurrentTenant, CurrentUser } from "./types.js";
import { useUser } from "./use-user.js";

const USER: CurrentUser = {
  id: "u1",
  email: "a@b.com",
  name: null,
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: [],
  mfaVerifiedAt: null,
};
const TENANT: CurrentTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

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
    ...overrides,
  };
}

describe("useUser", () => {
  it("returns the current user when authenticated", () => {
    const { result } = renderHook(() => useUser(), {
      wrapper: ({ children }: { children: ReactNode }) => (
        <AuthContext.Provider value={authValue()}>{children}</AuthContext.Provider>
      ),
    });
    expect(result.current).toBe(USER);
  });

  it("throws when no user is authenticated", () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <AuthContext.Provider value={authValue({ user: null, isAuthenticated: false })}>{children}</AuthContext.Provider>
    );
    expect(() => renderHook(() => useUser(), { wrapper })).toThrow(/must be called within an authenticated route/);
  });
});
