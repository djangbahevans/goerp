import { act, cleanup, render, waitFor } from "@testing-library/react";
import { useContext } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { AuthContextValue } from "./types.js";

function me(roles: string[]) {
  return {
    user: {
      id: "u1",
      email: "ada@example.com",
      contact_id: null,
      name: "Ada",
      avatar_url: null,
      roles,
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
      default_currency: "",
      available_locales: ["en"],
      first_day_of_week: "monday",
      number_format: "1,234.56",
    },
  };
}

function ok(body: unknown): Response {
  return { ok: true, status: 200, statusText: "", headers: new Headers(), json: async () => body } as Response;
}

beforeEach(() => {
  vi.resetModules();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function mountWithPendingReloads() {
  const pending: ((response: Response) => void)[] = [];
  let first = true;
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      if (url !== "/auth/me") throw new Error(`unexpected fetch ${url}`);
      if (first) {
        first = false;
        return Promise.resolve(ok(me(["sales"])));
      }
      return new Promise<Response>((resolve) => pending.push(resolve));
    }),
  );
  const provider = await import("./auth-provider.js");
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
  const current = () => auth as unknown as AuthContextValue;
  await waitFor(() => expect(current().state.status).toBe("authenticated"));
  return { pending, current };
}

it("keeps an earlier reload's session when the later reload fails", async () => {
  const { pending, current } = await mountWithPendingReloads();
  let older!: Promise<void>;
  let newer!: Promise<void>;
  act(() => {
    older = current().reloadSession();
    newer = current().reloadSession();
  });
  await waitFor(() => expect(pending).toHaveLength(2));
  await act(async () => {
    pending[0]?.(ok(me(["warehouse"])));
    await older;
  });
  await act(async () => {
    pending[1]?.({
      ok: false,
      status: 503,
      statusText: "",
      headers: new Headers(),
      json: async () => ({}),
    } as Response);
    await newer.catch(() => {});
  });

  expect(current().user?.roles).toEqual(["warehouse"]);
});

it("drops a reload response older than one already applied", async () => {
  const { pending, current } = await mountWithPendingReloads();
  let older!: Promise<void>;
  let newer!: Promise<void>;
  act(() => {
    older = current().reloadSession();
    newer = current().reloadSession();
  });
  await waitFor(() => expect(pending).toHaveLength(2));
  await act(async () => {
    pending[1]?.(ok(me(["warehouse"])));
    await newer;
  });
  await act(async () => {
    pending[0]?.(ok(me(["sales", "revoked"])));
    await older;
  });

  expect(current().user?.roles).toEqual(["warehouse"]);
});
