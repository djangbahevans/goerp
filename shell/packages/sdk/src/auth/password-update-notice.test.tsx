import { act, cleanup, render, renderHook, waitFor } from "@testing-library/react";
import { useContext } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthContextValue } from "./types.js";

const STORAGE_KEY = "goerp-password-update-recommended";

const ME_BODY = {
  user: {
    id: "u1",
    email: "ada@example.com",
    contact_id: null,
    name: "Ada",
    avatar_url: null,
    roles: [],
    amr: ["pwd"],
    mfa_verified_at: null,
    theme: "system",
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

// Routes each auth endpoint to a canned response; signedIn flips /auth/me
// from 401 to 200 once a sign-in has succeeded.
function stubAuthServer(responses: { login?: unknown; verify?: unknown; handoff?: unknown }) {
  let signedIn = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      switch (url) {
        case "/auth/me":
          return signedIn ? json(200, ME_BODY) : json(401, {});
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
    expect(window.sessionStorage.getItem(STORAGE_KEY)).toBe("1");
    expect(listener).toHaveBeenCalledTimes(1);

    store.set(false);
    expect(window.sessionStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("restores the flag after a reload", async () => {
    window.sessionStorage.setItem(STORAGE_KEY, "1");
    const { PasswordUpdateNoticeStore } = await import("./password-update-notice.js");
    expect(new PasswordUpdateNoticeStore().get()).toBe(true);
  });

  it("usePasswordUpdateNotice dismisses", async () => {
    const { passwordUpdateNotice, usePasswordUpdateNotice } = await import("./password-update-notice.js");
    passwordUpdateNotice.set(true);
    const { result } = renderHook(() => usePasswordUpdateNotice());
    expect(result.current.recommended).toBe(true);

    act(() => result.current.dismiss());
    expect(result.current.recommended).toBe(false);
  });
});

describe("AuthProvider and the password update notice", () => {
  it("sets the flag from a password sign-in and clears it on logout", async () => {
    stubAuthServer({ login: { expires_in: 900, password_update_recommended: true } });
    const { auth, notice } = await mountProvider();

    await act(() => auth().login({ email: "ada@example.com", password: "pw", tenant: "acme" }));
    expect(notice.get()).toBe(true);

    await act(() => auth().logout());
    await waitFor(() => expect(notice.get()).toBe(false));
  });

  it("keeps the flag across a reload that finds a live session", async () => {
    window.sessionStorage.setItem(STORAGE_KEY, "1");
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
    window.sessionStorage.setItem(STORAGE_KEY, "1");
    stubAuthServer({ login: { expires_in: 900 } });
    const { notice } = await mountProvider();

    expect(notice.get()).toBe(false);
  });

  it("sets the flag from an MFA verify", async () => {
    stubAuthServer({
      login: { mfa_required: true, mfa_token: "tok", mfa_methods: ["totp"] },
      verify: { expires_in: 900, password_update_recommended: true },
    });
    const { auth, notice } = await mountProvider();

    await act(() => auth().login({ email: "ada@example.com", password: "pw", tenant: "acme" }));
    await act(() => auth().submitMFA("123456"));
    expect(notice.get()).toBe(true);
  });

  it("clears the flag after a password change", async () => {
    stubAuthServer({ login: { expires_in: 900, password_update_recommended: true } });
    const { auth, notice } = await mountProvider();

    await act(() => auth().login({ email: "ada@example.com", password: "pw", tenant: "acme" }));
    await act(() => auth().changePassword({ currentPassword: "pw", newPassword: "a new passphrase" }));
    expect(notice.get()).toBe(false);
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
    stubAuthServer({ handoff: { expires_in: 900, password_update_recommended: true } });
    const { auth, notice } = await mountProvider();

    await act(() => auth().completeHandoff("c0de"));

    expect(auth().state.status).toBe("authenticated");
    expect(notice.get()).toBe(true);
  });
});
