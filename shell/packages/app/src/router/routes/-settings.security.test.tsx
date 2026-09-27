import type { AuthContextValue, AuthState, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { toast } from "@goerp/sdk/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { type ReactNode, useState } from "react";
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
  expireSession: () => {},
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

interface FactorWire {
  id: string;
  type: "totp" | "webauthn";
  label: string | null;
  created_at: string;
  last_used_at: string | null;
}

interface MFAOptions {
  factors?: FactorWire[];
  recoveryCodes?: string[];
  requiredByPolicy?: boolean;
  // The first enrollment call answers 403 mfa_reverify_required.
  reverifyFirst?: boolean;
}

const TOTP_CODE = "123456";
const TEN_CODES = Array.from({ length: 10 }, (_, i) => `NEWCD-${String(i).padStart(5, "A")}`);

function factor(id: string, overrides: Partial<FactorWire> = {}): FactorWire {
  return {
    id,
    type: "totp",
    label: "iPhone",
    created_at: "2026-08-01T10:00:00Z",
    last_used_at: null,
    ...overrides,
  };
}

// An in-memory GET/DELETE /auth/sessions and /auth/mfa/* with the engine's
// wire shapes and status codes (internal/engine/auth/authsessions,
// internal/engine/auth/mfafactors, internal/engine/auth/mfaenroll).
function stubSessionsBackend(initial: SessionWire[], options: { failList?: boolean; mfa?: MFAOptions } = {}) {
  let sessions = [...initial];
  let failList = options.failList ?? false;
  let factors = [...(options.mfa?.factors ?? [])];
  let recoveryCodes = [...(options.mfa?.recoveryCodes ?? [])];
  const requiredByPolicy = options.mfa?.requiredByPolicy ?? false;
  let reverifyPending = options.mfa?.reverifyFirst ?? false;
  const bodies: { url: string; body: unknown }[] = [];

  const verify = (body: { type: string; code: string }): boolean => {
    if (body.type === "totp") return body.code === TOTP_CODE;
    if (body.type === "recovery_code" && recoveryCodes.includes(body.code)) {
      recoveryCodes = recoveryCodes.filter((c) => c !== body.code);
      return true;
    }
    return false;
  };
  const invalidCode = () => json(401, { error: { code: "invalid_mfa_code", message: "invalid MFA code" } });

  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(init.body as string) : undefined;
    if (body !== undefined) bodies.push({ url, body });
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
    if (url === "/auth/mfa/factors") {
      return json(200, {
        factors,
        recovery_codes_remaining: recoveryCodes.length,
        required_by_policy: requiredByPolicy,
      });
    }
    const remove = url.match(/^\/auth\/mfa\/factors\/(.+)\/remove$/);
    if (remove) {
      if (!factors.some((f) => f.id === remove[1])) {
        return json(404, { error: { code: "mfa_factor_not_found", message: "MFA factor not found" } });
      }
      if (requiredByPolicy && factors.length === 1) {
        return json(409, { error: { code: "mfa_required_by_policy", message: "required" } });
      }
      if (!verify(body)) return invalidCode();
      factors = factors.filter((f) => f.id !== remove[1]);
      if (factors.length === 0) recoveryCodes = [];
      return new Response(null, { status: 204 });
    }
    if (url === "/auth/mfa/recovery-codes/regenerate") {
      if (factors.length === 0) return json(409, { error: { code: "mfa_not_enrolled", message: "not enrolled" } });
      if (!verify(body)) return invalidCode();
      recoveryCodes = [...TEN_CODES];
      return json(200, { recovery_codes: recoveryCodes });
    }
    if (url === "/auth/mfa/reverify") {
      if (!verify(body)) return invalidCode();
      reverifyPending = false;
      return json(200, { expires_in: 900 });
    }
    if (url === "/auth/mfa/enroll/totp") {
      if (reverifyPending) {
        return json(403, { error: { code: "mfa_reverify_required", message: "verify your existing MFA factor" } });
      }
      return json(200, {
        enrollment_id: "enr-1",
        qr_svg: "<svg xmlns='http://www.w3.org/2000/svg'/>",
        secret: "JBSWY3DPEHPK3PXP",
      });
    }
    if (url === "/auth/mfa/enroll/totp/confirm") {
      if (body.code !== TOTP_CODE) return json(400, { error: { code: "invalid_mfa_code", message: "invalid" } });
      factors = [...factors, factor(`f-${factors.length + 1}`, { label: body.label ?? null })];
      const issued = recoveryCodes.length === 0 ? [...TEN_CODES] : null;
      if (issued) recoveryCodes = issued;
      return json(200, { recovery_codes: issued, expires_in: 900 });
    }
    return json(404, { error: { code: "not_found", message: "not found" } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return {
    sessions: () => sessions,
    factors: () => factors,
    recoveryCodes: () => recoveryCodes,
    bodies: (url: string) => bodies.filter((b) => b.url === url).map((b) => b.body),
    recoverList: () => {
      failList = false;
    },
    listCalls: () => fetchMock.mock.calls.filter(([url, init]) => url === "/auth/sessions" && !init?.method).length,
  };
}

// Stand-in for AuthProvider whose expireSession moves to the expired state.
function StatefulAuth({ children, onExpire }: { children: ReactNode; onExpire: () => void }) {
  const [state, setState] = useState<AuthState>(AUTH.state);
  const isAuthenticated = state.status === "authenticated";
  const value: AuthContextValue = {
    ...AUTH,
    state,
    isAuthenticated,
    expireSession: () => {
      onExpire();
      setState({ status: "unauthenticated", sessionExpired: true, user: USER, tenant: TENANT });
    },
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

async function renderSecurityPage(onExpire: () => void = () => {}) {
  const router = createRouter({
    routeTree,
    context: { auth: AUTH },
    history: createMemoryHistory({ initialEntries: ["/settings/security"] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <StatefulAuth onExpire={onExpire}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
        </PermissionContext.Provider>
      </StatefulAuth>
    </QueryClientProvider>,
  );
  return router;
}

function section(title: string): HTMLElement {
  const heading = screen.getByRole("heading", { name: title });
  return heading.closest("section") as HTMLElement;
}

function rows(): HTMLElement[] {
  return within(section("Active sessions")).getAllByRole("row").slice(1);
}

function factorRows(): HTMLElement[] {
  return within(section("Two-factor authentication")).getAllByRole("row").slice(1);
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

describe("/settings/security two-factor authentication", () => {
  const ONE_FACTOR: MFAOptions = { factors: [factor("f-1")], recoveryCodes: ["AAAAA-BBBBB", "CCCCC-DDDDD"] };

  // Records whether text ever appeared in the document, however briefly.
  function watchForText(text: string): () => boolean {
    let seen = false;
    const observer = new MutationObserver(() => {
      if (document.body.textContent?.includes(text)) seen = true;
    });
    observer.observe(document.body, { childList: true, subtree: true, characterData: true });
    return () => {
      observer.disconnect();
      return seen;
    };
  }

  function typeCode(container: HTMLElement, code: string) {
    fireEvent.change(within(container).getByLabelText("Verification code"), { target: { value: code } });
  }

  it("shows 2FA off with an Add authenticator app button and no recovery codes", async () => {
    stubSessionsBackend([session("fam-this", { current: true })]);
    await renderSecurityPage();

    const twoFactor = section("Two-factor authentication");
    await within(twoFactor).findByText("Off");
    expect(within(twoFactor).getByRole("button", { name: "Add authenticator app" })).toBeTruthy();
    expect(within(twoFactor).queryByRole("table")).toBeNull();
    expect(within(twoFactor).queryByText(/recovery codes? left/)).toBeNull();
  });

  it("lists a factor with its type, name, added date and last use, and the recovery code count", async () => {
    stubSessionsBackend([session("fam-this", { current: true })], { mfa: ONE_FACTOR });
    await renderSecurityPage();

    await waitFor(() => expect(factorRows()).toHaveLength(1));
    const row = factorRows()[0] as HTMLElement;
    expect(within(row).getByText("Authenticator app")).toBeTruthy();
    expect(within(row).getByText("iPhone")).toBeTruthy();
    expect(within(row).getByText("Never")).toBeTruthy();
    expect(within(row).getByRole("button", { name: "Remove iPhone" })).toBeTruthy();
    const twoFactor = section("Two-factor authentication");
    expect(within(twoFactor).getByText("On")).toBeTruthy();
    expect(within(twoFactor).getByText(/2 recovery codes left/)).toBeTruthy();
  });

  it("offers no Remove on the last factor under a required policy", async () => {
    stubSessionsBackend([session("fam-this", { current: true })], { mfa: { ...ONE_FACTOR, requiredByPolicy: true } });
    await renderSecurityPage();

    await waitFor(() => expect(factorRows()).toHaveLength(1));
    const row = factorRows()[0] as HTMLElement;
    expect(within(row).queryByRole("button", { name: /Remove/ })).toBeNull();
    expect(within(row).getByText("Your organisation requires two-factor authentication")).toBeTruthy();
  });

  it("removes a factor with a TOTP code, expires the session, and lands on sign-in", async () => {
    const onExpire = vi.fn();
    const expiredModalSeen = watchForText("Your session has expired");
    const backend = stubSessionsBackend([session("fam-this", { current: true })], { mfa: ONE_FACTOR });
    const router = await renderSecurityPage(onExpire);

    await waitFor(() => expect(factorRows()).toHaveLength(1));
    fireEvent.click(within(factorRows()[0] as HTMLElement).getByRole("button", { name: "Remove iPhone" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/signed out everywhere, including this browser/)).toBeTruthy();
    typeCode(dialog, TOTP_CODE);

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(expiredModalSeen()).toBe(false);
    expect(onExpire).toHaveBeenCalledOnce();
    expect(router.state.location.search).toMatchObject({ notice: "mfa_factor_removed" });
    expect(backend.factors()).toHaveLength(0);
    expect(backend.recoveryCodes()).toHaveLength(0);
    expect(backend.bodies("/auth/mfa/factors/f-1/remove")).toEqual([{ type: "totp", code: TOTP_CODE }]);
  });

  it("removes a factor with a normalized recovery code", async () => {
    const backend = stubSessionsBackend([session("fam-this", { current: true })], {
      mfa: { ...ONE_FACTOR, factors: [factor("f-1"), factor("f-2", { label: "iPad" })] },
    });
    const router = await renderSecurityPage();

    await waitFor(() => expect(factorRows()).toHaveLength(2));
    fireEvent.click(screen.getByRole("button", { name: "Remove iPad" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Use a recovery code instead" }));
    fireEvent.change(within(dialog).getByLabelText("Recovery code"), { target: { value: "ccccc ddddd" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/auth/login"));
    expect(backend.bodies("/auth/mfa/factors/f-2/remove")).toEqual([{ type: "recovery_code", code: "CCCCC-DDDDD" }]);
    expect(backend.factors().map((f) => f.id)).toEqual(["f-1"]);
  });

  it("shows an invalid code inline and keeps the dialog open", async () => {
    const onExpire = vi.fn();
    const backend = stubSessionsBackend([session("fam-this", { current: true })], { mfa: ONE_FACTOR });
    await renderSecurityPage(onExpire);

    await waitFor(() => expect(factorRows()).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Remove iPhone" }));
    const dialog = await screen.findByRole("alertdialog");
    typeCode(dialog, "000000");

    expect(await within(dialog).findByText("Incorrect code. Check your authenticator app and try again.")).toBeTruthy();
    expect(screen.getByRole("alertdialog")).toBeTruthy();
    expect(onExpire).not.toHaveBeenCalled();
    expect(backend.factors()).toHaveLength(1);
  });

  it("regenerates recovery codes, shows the ten once, and updates the count to 10", async () => {
    const backend = stubSessionsBackend([session("fam-this", { current: true }), session("fam-phone")], {
      mfa: ONE_FACTOR,
    });
    await renderSecurityPage();
    await waitFor(() => expect(rows()).toHaveLength(2));
    const listsBefore = backend.listCalls();

    fireEvent.click(await screen.findByRole("button", { name: "Generate new codes" }));
    const dialog = await screen.findByRole("alertdialog");
    typeCode(dialog, TOTP_CODE);

    const list = await within(dialog).findByRole("list", { name: "Recovery codes" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(10);
    const done = within(dialog).getByRole("button", { name: "Done" }) as HTMLButtonElement;
    expect(done.disabled).toBe(true);
    fireEvent.click(within(dialog).getByLabelText("I've saved these codes"));
    expect(done.disabled).toBe(false);
    fireEvent.click(done);

    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(within(section("Two-factor authentication")).getByText(/10 recovery codes left/)).toBeTruthy();
    // Regenerating signs out the other sessions, so the list is refetched.
    expect(backend.listCalls()).toBeGreaterThan(listsBefore);
  });

  it("adds a second authenticator app without a recovery-codes step", async () => {
    const toastSuccess = vi.spyOn(toast, "success").mockImplementation(() => "");
    const backend = stubSessionsBackend([session("fam-this", { current: true })], { mfa: ONE_FACTOR });
    await renderSecurityPage();

    fireEvent.click(await screen.findByRole("button", { name: "Add authenticator app" }));
    const sheet = await screen.findByRole("dialog", { name: "Add authenticator app" });
    await within(sheet).findByAltText(/QR code/);
    fireEvent.change(within(sheet).getByLabelText("Name"), { target: { value: "Work phone" } });
    typeCode(sheet, TOTP_CODE);

    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Authenticator app added."));
    expect(screen.queryByRole("list", { name: "Recovery codes" })).toBeNull();
    await waitFor(() => expect(factorRows()).toHaveLength(2));
    expect(backend.factors()[1]?.label).toBe("Work phone");
  });

  it("shows the recovery codes when the first authenticator app is added", async () => {
    stubSessionsBackend([session("fam-this", { current: true })]);
    await renderSecurityPage();

    fireEvent.click(await screen.findByRole("button", { name: "Add authenticator app" }));
    const sheet = await screen.findByRole("dialog", { name: "Add authenticator app" });
    await within(sheet).findByAltText(/QR code/);
    typeCode(sheet, TOTP_CODE);

    const list = await within(sheet).findByRole("list", { name: "Recovery codes" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(10);

    // Shown only once, so the sheet won't close until they're marked saved.
    fireEvent.click(within(sheet).getByRole("button", { name: "Close" }));
    expect(screen.getByRole("dialog", { name: "Add authenticator app" })).toBeTruthy();
    fireEvent.click(within(sheet).getByLabelText("I've saved these codes"));
    fireEvent.click(within(sheet).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add authenticator app" })).toBeNull());
  });

  it("asks for a current code when enrollment needs re-verification, then continues", async () => {
    const backend = stubSessionsBackend([session("fam-this", { current: true })], {
      mfa: { ...ONE_FACTOR, reverifyFirst: true },
    });
    await renderSecurityPage();

    fireEvent.click(await screen.findByRole("button", { name: "Add authenticator app" }));
    const sheet = await screen.findByRole("dialog", { name: "Add authenticator app" });
    await within(sheet).findByText(/Confirm it's you/);
    await act(async () => typeCode(sheet, TOTP_CODE));

    await within(sheet).findByAltText(/QR code/);
    expect(backend.bodies("/auth/mfa/reverify")).toEqual([{ type: "totp", code: TOTP_CODE }]);
  });
});
