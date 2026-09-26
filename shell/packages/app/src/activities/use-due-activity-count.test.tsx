import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext } from "@goerp/sdk/auth";
import { useMyActivities } from "@goerp/sdk/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { addDays, todayIn } from "./activity-dates.js";
import { type FakeMyActivities, installFakeMyActivities } from "./fake-my-activities.js";
import { MY_ACTIVITIES_PAGE_SIZE, useActivityTimezone, useDueActivityCount } from "./use-due-activity-count.js";

let fake: FakeMyActivities | undefined;
afterEach(() => {
  fake?.restore();
  fake = undefined;
});

function auth(timezone: string | null, defaultTimezone = "UTC"): AuthContextValue {
  const user = {
    id: "u1",
    email: "ama@example.com",
    name: "Ama",
    contactId: null,
    avatarUrl: null,
    roles: [],
    amr: [],
    mfaVerifiedAt: null,
    mfaSetupRequired: false,
    theme: "system" as const,
    locale: null,
    timezone,
    dateFormat: null,
  };
  const tenant = {
    id: "t1",
    slug: "acme",
    name: "Acme",
    plan: "pro",
    defaultLocale: "en",
    defaultTimezone,
    availableLocales: ["en"],
  };
  return {
    state: { status: "authenticated", user, tenant },
    isAuthenticated: true,
    user,
    tenant,
    login: vi.fn(),
    logout: vi.fn(),
    submitMFA: vi.fn(),
    updateProfile: vi.fn(),
    updatePreferences: vi.fn(),
    changePassword: vi.fn(),
    reloadSession: vi.fn(),
  };
}

function wrapper(value: AuthContextValue = auth(null)) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
    </QueryClientProvider>
  );
}

describe("useActivityTimezone", () => {
  it("uses the user's own timezone, then the tenant default", () => {
    expect(renderHook(useActivityTimezone, { wrapper: wrapper(auth("Asia/Tokyo")) }).result.current).toBe("Asia/Tokyo");
    expect(renderHook(useActivityTimezone, { wrapper: wrapper(auth(null, "Africa/Accra")) }).result.current).toBe(
      "Africa/Accra",
    );
  });
});

describe("useDueActivityCount", () => {
  it("loads pages until one ends past today and counts overdue plus due-today activities", async () => {
    const today = todayIn("UTC");
    const due = Array.from({ length: MY_ACTIVITIES_PAGE_SIZE + 10 }, () => addDays(today, -1));
    const later = Array.from({ length: MY_ACTIVITIES_PAGE_SIZE * 2 }, () => addDays(today, 3));
    fake = installFakeMyActivities([...due, today, ...later].map((dueDate) => ({ dueDate })));

    const { result } = renderHook(useDueActivityCount, { wrapper: wrapper() });

    await waitFor(() => expect(result.current).toBe(MY_ACTIVITIES_PAGE_SIZE + 11));
    // The second page ends past today, so the third is never requested.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(fake.requests).toHaveLength(2);
  });

  it("drops the count after an activity is marked done", async () => {
    const today = todayIn("UTC");
    fake = installFakeMyActivities([
      { dueDate: addDays(today, -2) },
      { dueDate: today },
      { dueDate: addDays(today, 2) },
    ]);

    const { result } = renderHook(
      () => ({ count: useDueActivityCount(), page: useMyActivities({ limit: MY_ACTIVITIES_PAGE_SIZE }) }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.count).toBe(2));

    await act(async () => {
      await result.current.page.markDone("s0001");
    });

    await waitFor(() => expect(result.current.count).toBe(1));
  });
});
