import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { toast } from "@goerp/sdk/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

const TENANT = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
  passwordMinLength: 12,
};

const USER: CurrentUser = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system",
  locale: null,
  timezone: null,
  dateFormat: null,
};

const AUTH: AuthContextValue = {
  state: { status: "authenticated", user: USER, tenant: TENANT },
  isAuthenticated: true,
  user: USER,
  tenant: TENANT,
  login: async () => null,
  completeHandoff: async () => {},
  selectTenant: async () => null,
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

interface SessionWire {
  id: string;
  user_agent: string | null;
  ip_address: string | null;
  country_code: string | null;
  signed_in_at: string;
  last_active_at: string;
  persistent: boolean;
  current: boolean;
}

function session(id: string, overrides: Partial<SessionWire> = {}): SessionWire {
  return {
    id,
    user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/537.36 Chrome/128.0.0.0 Safari/537.36",
    ip_address: "41.66.18.2",
    country_code: "GH",
    signed_in_at: "2026-09-01T10:00:00Z",
    last_active_at: new Date().toISOString(),
    persistent: true,
    current: false,
    ...overrides,
  };
}

function json(status: number, body: unknown): Response {
  return new Response(body === null ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// An in-memory GET/DELETE /auth/sessions with the engine's wire shapes and
// status codes (internal/engine/auth/authsessions).
function stubSessionsBackend(initial: SessionWire[], options: { failList?: boolean } = {}) {
  let sessions = [...initial];
  let failList = options.failList ?? false;
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    if (url === "/auth/sessions" && method === "GET") {
      if (failList) return json(500, { error: { code: "internal_error", message: "list sessions failed" } });
      return json(200, { sessions });
    }
    if (url === "/auth/sessions" && method === "DELETE") {
      const revoked = sessions.filter((s) => !s.current).length;
      sessions = sessions.filter((s) => s.current);
      return json(200, { revoked });
    }
    const one = url.match(/^\/auth\/sessions\/(.+)$/);
    if (one && method === "DELETE") {
      const target = sessions.find((s) => s.id === one[1]);
      if (!target) return json(404, { error: { code: "session_not_found", message: "session not found" } });
      if (target.current) {
        return json(400, { error: { code: "cannot_revoke_current_session", message: "sign out instead" } });
      }
      sessions = sessions.filter((s) => s.id !== target.id);
      return new Response(null, { status: 204 });
    }
    return json(404, { error: { code: "not_found", message: "not found" } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return {
    sessions: () => sessions,
    recoverList: () => {
      failList = false;
    },
    listCalls: () => fetchMock.mock.calls.filter(([url, init]) => url === "/auth/sessions" && !init?.method).length,
  };
}

async function renderSecurityPage() {
  const router = createRouter({
    routeTree,
    context: { auth: AUTH },
    history: createMemoryHistory({ initialEntries: ["/settings/security"] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AuthContext.Provider value={AUTH}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return router;
}

function rows(): HTMLElement[] {
  return screen.getAllByRole("row").slice(1);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("/settings/security", () => {
  it("renders inside the Settings rail with Security as the current page", async () => {
    stubSessionsBackend([session("fam-this", { current: true })]);
    await renderSecurityPage();

    const nav = screen.getByRole("navigation", { name: "Settings" });
    expect(within(nav).getByRole("link", { name: "Security" }).getAttribute("aria-current")).toBe("page");
    expect(screen.getByRole("heading", { name: "Security" })).toBeTruthy();
  });

  it("lists the current session first, badged and without a Sign out action", async () => {
    stubSessionsBackend([
      session("fam-this", { current: true }),
      session("fam-phone", {
        user_agent: "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/128.0.0.0 Mobile Safari/537.36",
        ip_address: "102.176.4.9",
        country_code: null,
      }),
    ]);
    await renderSecurityPage();

    await waitFor(() => expect(rows()).toHaveLength(2));
    const [current, other] = rows();
    expect(within(current as HTMLElement).getByText("Chrome on macOS")).toBeTruthy();
    expect(within(current as HTMLElement).getByText("This device")).toBeTruthy();
    expect(within(current as HTMLElement).queryByRole("button", { name: "Sign out" })).toBeNull();
    expect(within(other as HTMLElement).getByText("Chrome on Android")).toBeTruthy();
    expect(within(other as HTMLElement).getByText("102.176.4.9")).toBeTruthy();
    expect(within(other as HTMLElement).getByText("—")).toBeTruthy();
    expect(within(other as HTMLElement).getByRole("button", { name: "Sign out" })).toBeTruthy();
  });

  it("signs out another session after confirmation, and a refetch doesn't bring it back", async () => {
    const backend = stubSessionsBackend([session("fam-this", { current: true }), session("fam-phone")]);
    await renderSecurityPage();

    await waitFor(() => expect(rows()).toHaveLength(2));
    fireEvent.click(within(rows()[1] as HTMLElement).getByRole("button", { name: "Sign out" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Sign out" }));

    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(backend.sessions().map((s) => s.id)).toEqual(["fam-this"]);
    expect(screen.queryByRole("button", { name: "Sign out all other sessions" })).toBeNull();
  });

  it("keeps the session when the confirmation is cancelled", async () => {
    const backend = stubSessionsBackend([session("fam-this", { current: true }), session("fam-phone")]);
    await renderSecurityPage();

    await waitFor(() => expect(rows()).toHaveLength(2));
    fireEvent.click(within(rows()[1] as HTMLElement).getByRole("button", { name: "Sign out" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(rows()).toHaveLength(2);
    expect(backend.sessions()).toHaveLength(2);
  });

  it("signs out all other sessions, toasts the count, and leaves only the current row", async () => {
    const toastSuccess = vi.spyOn(toast, "success").mockImplementation(() => "");
    const backend = stubSessionsBackend([
      session("fam-this", { current: true }),
      session("fam-phone"),
      session("fam-tablet"),
    ]);
    await renderSecurityPage();

    fireEvent.click(await screen.findByRole("button", { name: "Sign out all other sessions" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Sign out all" }));

    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Signed out of 2 other sessions"));
    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(backend.listCalls()).toBe(2);
    expect(within(rows()[0] as HTMLElement).getByText("This device")).toBeTruthy();
    expect(screen.getByText("You're not signed in anywhere else.")).toBeTruthy();
  });

  it("shows an inline retry when the list fails, and recovers", async () => {
    const backend = stubSessionsBackend([session("fam-this", { current: true })], { failList: true });
    await renderSecurityPage();

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("Couldn't load your sessions.")).toBeTruthy();
    backend.recoverList();
    fireEvent.click(within(alert).getByRole("button", { name: "Retry" }));

    await waitFor(() => expect(rows()).toHaveLength(1));
  });

  it("links to the change password section on the profile page", async () => {
    stubSessionsBackend([session("fam-this", { current: true })]);
    await renderSecurityPage();

    expect(screen.getByRole("link", { name: "Change password" }).getAttribute("href")).toBe(
      "/settings/profile#change-password",
    );
  });
});
