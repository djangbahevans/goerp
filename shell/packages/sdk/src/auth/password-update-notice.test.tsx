import { act, cleanup, render, renderHook, waitFor } from "@testing-library/react";
import { useContext } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthContextValue } from "./types.js";

const STORAGE_KEY = "goerp-password-update-notice";
const DEADLINE = "2026-10-20T12:00:00Z";

const ME_BODY = {
  user: {
    id: "u1",
    email: "ada@example.com",
    contact_id: null as string | null,
    name: "Ada",
    avatar_url: null,
    roles: [],
    amr: ["pwd"],
    mfa_verified_at: null,
    theme: "system",
    contrast: "system",
    locale: null,
    timezone: null,
    date_format: null,
  },
  tenant: {
    id: "t1",
    slug: "acme",
    name: "Acme",
    plan: "pro",
    default_locale: "en",
    default_timezone: "UTC",
    available_locales: ["en"],
    first_day_of_week: "monday",
    number_format: "1,234.56",
  },
};

function json(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: "",
    headers: new Headers(),
    json: async () => body,
  } as Response;
}

function stubAuthServer(responses: { login?: unknown; verify?: unknown; handoff?: unknown; me?: typeof ME_BODY }) {
  let signedIn = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      switch (url) {
        case "/auth/me":
          return signedIn ? json(200, responses.me ?? ME_BODY) : json(401, {});
        case "/auth/login": {
          const body = responses.login as { mfa_required?: boolean; handoff?: unknown };
          if (!body.mfa_required && !body.handoff) signedIn = true;
          return json(200, responses.login);
        }
        case "/auth/handoff":
          signedIn = true;
          return json(200, responses.handoff);
        case "/auth/mfa/verify":
          signedIn = true;
          return json(200, responses.verify);
        case "/auth/me/change-password":
          return json(200, { status: "ok" });
        case "/auth/logout":
          signedIn = false;
          return json(200, {});
        default:
          throw new Error(`unexpected fetch ${url}`);
      }
    }),
  );
}

// The auth machine and the notice store are module singletons, so every test
// gets a fresh module graph.
async function mountProvider() {
  const provider = await import("./auth-provider.js");
  const notice = await import("./password-update-notice.js");
  let auth: AuthContextValue | null = null;
  function Capture() {
    auth = useContext(provider.AuthContext);
    return null;
  }
  render(
    <provider.AuthProvider>
      <Capture />
    </provider.AuthProvider>,
  );
  await waitFor(() => expect(auth?.state.status).toBe("unauthenticated"));
  return {
    auth: () => {
      if (!auth) throw new Error("AuthProvider didn't render");
      return auth;
    },
    notice: notice.passwordUpdateNotice,
  };
}

beforeEach(() => {
  vi.resetModules();
  window.sessionStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("PasswordUpdateNoticeStore", () => {
  it("persists to sessionStorage and notifies subscribers", async () => {
    const { PasswordUpdateNoticeStore } = await import("./password-update-notice.js");
    const store = new PasswordUpdateNoticeStore();
    const listener = vi.fn();
    store.subscribe(listener);

    store.set(true);
    expect(store.get()).toBe(true);
    expect(window.sessionStorage.getItem(STORAGE_KEY)).toBe(JSON.stringify({ recommended: true, deadline: null }));
    expect(listener).toHaveBeenCalledTimes(1);

    store.set(false);
    expect(window.sessionStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("restores the flag after a reload", async () => {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ recommended: true, deadline: DEADLINE }));
    const { PasswordUpdateNoticeStore } = await import("./password-update-notice.js");
    expect(new PasswordUpdateNoticeStore().getSnapshot()).toEqual({ recommended: true, deadline: DEADLINE });
  });

  it("usePasswordUpdateNotice dismisses", async () => {
    const { passwordUpdateNotice, usePasswordUpdateNotice } = await import("./password-update-notice.js");
    passwordUpdateNotice.set(true, DEADLINE);
    const { result } = renderHook(() => usePasswordUpdateNotice());
    expect(result.current.deadline).toBe(DEADLINE);
    expect(result.current.recommended).toBe(true);

    act(() => result.current.dismiss());
    expect(result.current.recommended).toBe(false);
    expect(result.current.deadline).toBeNull();
  });
});

describe("AuthProvider and the password update notice", () => {
  it("sets the flag from a password sign-in and clears it on logout", async () => {
    stubAuthServer({
      login: { expires_in: 900, password_update_recommended: true, password_update_deadline: DEADLINE },
    });
    const { auth, notice } = await mountProvider();

    await act(() => auth().login({ email: "ada@example.com", password: "pw", tenant: "acme" }));
    expect(notice.getSnapshot()).toEqual({ recommended: true, deadline: DEADLINE });

    await act(() => auth().logout());
    await waitFor(() => expect(notice.get()).toBe(false));
  });

  it("keeps the flag across a reload that finds a live session", async () => {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ recommended: true, deadline: null }));
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => json(200, ME_BODY)),
    );
    const provider = await import("./auth-provider.js");
    const { passwordUpdateNotice } = await import("./password-update-notice.js");
    let status = "";
    function Capture() {
      status = useContext(provider.AuthContext)?.state.status ?? "";
      return null;
    }
    render(
      <provider.AuthProvider>
        <Capture />
      </provider.AuthProvider>,
    );

    await waitFor(() => expect(status).toBe("authenticated"));
    expect(passwordUpdateNotice.get()).toBe(true);
  });

  it("drops the flag when a reload finds no session", async () => {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ recommended: true, deadline: null }));
    stubAuthServer({ login: { expires_in: 900 } });
    const { notice } = await mountProvider();

    expect(notice.getSnapshot()).toEqual({ recommended: false, deadline: null });
  });

  it("sets the flag from an MFA verify", async () => {
    stubAuthServer({
      login: { mfa_required: true, mfa_token: "tok", mfa_methods: ["totp"] },
      verify: { expires_in: 900, password_update_recommended: true, password_update_deadline: DEADLINE },
    });
    const { auth, notice } = await mountProvider();

    await act(() => auth().login({ email: "ada@example.com", password: "pw", tenant: "acme" }));
    await act(() => auth().submitMFA({ type: "totp", code: "123456" }));
    expect(notice.getSnapshot()).toEqual({ recommended: true, deadline: DEADLINE });
  });

  it("clears the flag after a password change", async () => {
    stubAuthServer({
      login: { expires_in: 900, password_update_recommended: true, password_update_deadline: DEADLINE },
    });
    const { auth, notice } = await mountProvider();

    await act(() => auth().login({ email: "ada@example.com", password: "pw", tenant: "acme" }));
    await act(() => auth().changePassword({ currentPassword: "pw", newPassword: "a new passphrase" }));
    expect(notice.getSnapshot()).toEqual({ recommended: false, deadline: null });
  });
});

describe("AuthProvider and the shared-domain handoff", () => {
  it("resolves a shared-domain login to its handoff and stays signed out", async () => {
    stubAuthServer({ login: { handoff: { host: "acme.localhost", code: "c0de" } } });
    const { auth } = await mountProvider();

    let handoff: unknown;
    await act(async () => {
      handoff = await auth().login({ email: "ada@example.com", password: "pw", tenant: "acme" });
    });

    expect(handoff).toEqual({ host: "acme.localhost", code: "c0de" });
    expect(auth().state.status).toBe("unauthenticated");
  });

  it("signs in on the tenant's host by exchanging the code", async () => {
    stubAuthServer({
      handoff: { expires_in: 900, password_update_recommended: true, password_update_deadline: DEADLINE },
    });
    const { auth, notice } = await mountProvider();

    await act(() => auth().completeHandoff("c0de"));

    expect(auth().state.status).toBe("authenticated");
    expect(notice.getSnapshot()).toEqual({ recommended: true, deadline: DEADLINE });
  });
});

it.each(["password", "mfa", "handoff"] as const)(
  "loads a restricted session after %s sign-in and clears it on reload",
  async (mode) => {
    const me = {
      ...ME_BODY,
      user: {
        ...ME_BODY.user,
        contact_id: "contact-current-tenant",
        password_change_required: true,
        password_min_length: 18,
      },
    };
    const success = { expires_in: 900, password_update_recommended: true, password_update_deadline: DEADLINE };
    stubAuthServer({
      me,
      login: mode === "mfa" ? { mfa_required: true, mfa_token: "tok", mfa_methods: ["totp"] } : success,
      verify: success,
      handoff: success,
    });
    const { auth, notice } = await mountProvider();
    if (mode === "handoff") await act(() => auth().completeHandoff("code"));
    else {
      await act(() => auth().login({ email: "ada@example.com", password: "pw" }));
      if (mode === "mfa") await act(() => auth().submitMFA({ type: "totp", code: "123456" }));
    }
    expect(auth().user).toMatchObject({
      passwordChangeRequired: true,
      passwordMinLength: 18,
      contactId: "contact-current-tenant",
    });
    expect(notice.getSnapshot()).toEqual({ recommended: true, deadline: DEADLINE });
    await act(() => auth().changePassword({ currentPassword: "pw", newPassword: "a new long passphrase" }));
    expect(notice.getSnapshot()).toEqual({ recommended: false, deadline: null });
    me.user.password_change_required = false;
    await act(() => auth().reloadSession());
    expect(auth().user?.passwordChangeRequired).toBe(false);
  },
);
