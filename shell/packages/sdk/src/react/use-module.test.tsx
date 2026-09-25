import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, expectTypeOf, it, vi } from "vitest";
import { AuthContext } from "../auth/auth-provider.js";
import { createPermissionContextValue, PermissionContext } from "../auth/permission-provider.js";
import type { AuthContextValue, CurrentTenant, CurrentUser } from "../auth/types.js";
import { usePermission } from "../auth/use-permission.js";
import { defineModule } from "../module/define-module.js";
import { toast } from "../notifications/toast.js";
import { ModuleNavigationProvider, type NavigateFn, useModule } from "./use-module.js";
import { useToast } from "./use-toast.js";

const user: CurrentUser = {
  id: "u1",
  email: "ada@acme.test",
  name: "Ada",
  contactId: null,
  avatarUrl: null,
  roles: ["user"],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
};
const tenant: CurrentTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };
const auth: AuthContextValue = {
  state: { status: "authenticated", user, tenant },
  isAuthenticated: true,
  user,
  tenant,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};
const permissions = createPermissionContextValue({
  permissions: new Set(["contacts:contact:read"]),
  fieldAccess: {},
  modulesEnabled: new Set(["contacts"]),
});

interface Contact {
  id: string;
  name: string;
}

const contactsApi = {
  listContacts: async (_params?: { limit?: number }): Promise<Contact[]> => [],
};

declare module "./use-module.js" {
  interface ModuleApis {
    contacts: typeof contactsApi;
  }
}

afterEach(cleanup);

function shell(navigate: NavigateFn, queryClient = new QueryClient()) {
  return function Shell({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={auth}>
          <PermissionContext.Provider value={permissions}>
            <ModuleNavigationProvider navigate={navigate}>{children}</ModuleNavigationProvider>
          </PermissionContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>
    );
  };
}

describe("useModule", () => {
  it("returns the signed-in user and tenant", () => {
    const { result } = renderHook(() => useModule("contacts"), { wrapper: shell(vi.fn()) });
    expect(result.current.user).toBe(user);
    expect(result.current.tenant).toBe(tenant);
  });

  it("checks permissions the same way usePermission does", () => {
    const { result } = renderHook(
      () => ({
        ctx: useModule("contacts"),
        read: usePermission("contacts:contact:read"),
        write: usePermission("contacts:contact:write"),
      }),
      { wrapper: shell(vi.fn()) },
    );
    expect(result.current.ctx.can("contacts:contact:read")).toBe(true);
    expect(result.current.ctx.can("contacts:contact:read")).toBe(result.current.read);
    expect(result.current.ctx.can("contacts:contact:write")).toBe(false);
    expect(result.current.ctx.can("contacts:contact:write")).toBe(result.current.write);
  });

  it("navigates through the shell's navigate", () => {
    const navigate = vi.fn<NavigateFn>();
    const { result } = renderHook(() => useModule("contacts"), { wrapper: shell(navigate) });
    result.current.navigate("/contacts?stage=lead");
    result.current.navigate("/contacts/c1", { replace: true });
    expect(navigate.mock.calls).toEqual([["/contacts?stage=lead"], ["/contacts/c1", { replace: true }]]);
  });

  it("hands over the surrounding QueryClient", () => {
    const queryClient = new QueryClient();
    const { result } = renderHook(() => useModule("contacts"), { wrapper: shell(vi.fn(), queryClient) });
    expect(result.current.queryClient).toBe(queryClient);
  });

  it("uses the shell-wide toast, the one useToast and @goerp/sdk/notifications return", () => {
    const { result } = renderHook(() => ({ ctx: useModule("contacts"), fromHook: useToast().toast }), {
      wrapper: shell(vi.fn()),
    });
    expect(result.current.ctx.toast).toBe(toast);
    expect(result.current.ctx.toast).toBe(result.current.fromHook);
  });

  it("keeps the same context object across re-renders", () => {
    const { result, rerender } = renderHook(() => useModule("contacts"), { wrapper: shell(vi.fn()) });
    const first = result.current;
    rerender();
    expect(result.current).toBe(first);
  });

  it("throws a descriptive error outside the shell's navigation provider", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    function Probe() {
      useModule("contacts");
      return null;
    }
    expect(() =>
      render(
        <AuthContext.Provider value={auth}>
          <PermissionContext.Provider value={permissions}>
            <Probe />
          </PermissionContext.Provider>
        </AuthContext.Provider>,
      ),
    ).toThrow(
      'useModule("contacts") must be called inside a module view the shell renders: no ModuleNavigationProvider above it',
    );
  });

  it("throws a descriptive error without a PermissionProvider", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    function Probe() {
      useModule("contacts");
      return null;
    }
    expect(() =>
      render(
        <ModuleNavigationProvider navigate={vi.fn()}>
          <Probe />
        </ModuleNavigationProvider>,
      ),
    ).toThrow(/no PermissionProvider above it/);
  });

  describe("api", () => {
    it("returns the client the module registered with defineModule", async () => {
      defineModule({ name: "contacts", api: contactsApi });
      const { result } = renderHook(() => useModule("contacts"), { wrapper: shell(vi.fn()) });
      expect(result.current.api).toBe(contactsApi);
      await expect(result.current.api.listContacts({ limit: 20 })).resolves.toEqual([]);
    });

    it("is undefined for a module that registered no api", () => {
      defineModule({ name: "use_module_test_no_api" });
      const { result } = renderHook(() => useModule("use_module_test_no_api"), { wrapper: shell(vi.fn()) });
      expect(result.current.api).toBeUndefined();
    });

    it("switches to the new client when a hot-reloaded module re-registers", () => {
      defineModule({ name: "use_module_test_reload", api: { version: 1 } });
      const { result } = renderHook(() => useModule("use_module_test_reload"), { wrapper: shell(vi.fn()) });
      const first = result.current;
      const replacement = { version: 2 };
      act(() => {
        defineModule({ name: "use_module_test_reload", api: replacement });
      });
      expect(result.current.api).toBe(replacement);
      expect(result.current).not.toBe(first);
    });

    it("types api from ModuleApis, and as unknown for a name outside it", () => {
      expectTypeOf<ReturnType<typeof useModule<"contacts">>["api"]>().toEqualTypeOf<typeof contactsApi>();
      expectTypeOf<ReturnType<typeof useModule<"contacts">>["api"]["listContacts"]>().toBeFunction();
      expectTypeOf<ReturnType<typeof useModule<"billing">>["api"]>().toBeUnknown();
    });
  });
});
