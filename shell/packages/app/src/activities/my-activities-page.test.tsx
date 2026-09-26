import { apiClient } from "@goerp/sdk";
import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { resourceMetadataRegistry, viewPathRegistry } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { addDays, dueLabel, todayIn } from "./activity-dates.js";
import { type FakeActivitySeed, type FakeMyActivities, installFakeMyActivities } from "./fake-my-activities.js";
import { MyActivitiesPage } from "./my-activities-page.js";

function fakeAuth(): AuthContextValue {
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
    timezone: "UTC",
    dateFormat: null,
  };
  const tenant = {
    id: "t1",
    slug: "acme",
    name: "Acme",
    plan: "pro",
    defaultLocale: "en",
    defaultTimezone: "UTC",
    availableLocales: ["en"],
  };
  return {
    state: { status: "authenticated", user, tenant },
    isAuthenticated: true,
    user,
    tenant,
    login: vi.fn(),
    completeHandoff: vi.fn(),
    logout: vi.fn(),
    submitMFA: vi.fn(),
    updateProfile: vi.fn(),
    updatePreferences: vi.fn(),
    changePassword: vi.fn(),
    reloadSession: vi.fn(),
  };
}

const permissionValue = createPermissionContextValue({
  permissions: new Set<string>(),
  fieldAccess: {},
  modulesEnabled: new Set<string>(),
});

async function renderPage() {
  const auth = fakeAuth();
  const rootRoute = createRootRoute({ component: Outlet });
  const pageRoute = createRoute({ getParentRoute: () => rootRoute, path: "/activities", component: MyActivitiesPage });
  const recordRoute = createRoute({ getParentRoute: () => rootRoute, path: "/_m/$", component: () => null });
  const router = createRouter({
    routeTree: rootRoute.addChildren([pageRoute, recordRoute]),
    history: createMemoryHistory({ initialEntries: ["/activities"] }),
  });
  await router.load();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth}>
        <PermissionContext.Provider value={permissionValue}>
          <RouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
  return router;
}

const today = todayIn("UTC");

let fake: FakeMyActivities | undefined;

function seed(seeds: FakeActivitySeed[]): FakeMyActivities {
  fake = installFakeMyActivities(seeds);
  return fake;
}

beforeEach(() => {
  vi.spyOn(resourceMetadataRegistry, "resolve").mockImplementation(async (model: string) =>
    model === "sales.order"
      ? ({ module: "sales", resource: model, defaultFormView: "order_form" } as never)
      : ({ module: "notes", resource: model, defaultFormView: "" } as never),
  );
  vi.spyOn(viewPathRegistry, "resolveRecord").mockResolvedValue("/orders/{id}");
});

afterEach(() => {
  cleanup();
  fake?.restore();
  fake = undefined;
  vi.restoreAllMocks();
});

function group(name: string) {
  return screen.getByRole("region", { name });
}

describe("MyActivitiesPage", () => {
  it("groups activities Overdue, Today and Upcoming and hides empty groups", async () => {
    seed([
      { dueDate: addDays(today, 2), summary: "Send the quote" },
      { dueDate: addDays(today, -1), summary: "Call the customer" },
      { dueDate: addDays(today, 1), summary: "Book the venue" },
    ]);
    await renderPage();

    await waitFor(() => expect(screen.getByRole("region", { name: "Overdue" })).toBeTruthy());
    expect(screen.queryByRole("region", { name: "Today" })).toBeNull();
    expect(within(group("Overdue")).getByText("Call the customer")).toBeTruthy();
    const upcoming = within(group("Upcoming"))
      .getAllByRole("listitem")
      .map((li) => li.textContent);
    expect(upcoming[0]).toContain("Book the venue");
    expect(upcoming[0]).toContain("Tomorrow");
    expect(upcoming[1]).toContain("Send the quote");
  });

  it("shows each row's type, summary, record link and due date, with overdue dates in the danger color", async () => {
    seed([
      {
        dueDate: addDays(today, -3),
        summary: "Chase the invoice",
        type: "email",
        recordId: "o7",
        recordName: "SO-0007",
      },
      { dueDate: today, summary: "Write notes", model: "notes.note", recordName: null, recordId: "n1" },
    ]);
    await renderPage();

    const overdueRow = await waitFor(() => within(group("Overdue")).getByRole("listitem"));
    expect(within(overdueRow).getByText("Email")).toBeTruthy();
    await waitFor(() =>
      expect(within(overdueRow).getByRole("link", { name: "SO-0007" }).getAttribute("href")).toBe("/_m/orders/o7"),
    );
    // One copy per layout: beside the summary from sm up, on the record line below it.
    const dates = within(overdueRow).getAllByText(dueLabel(addDays(today, -3), today));
    expect(dates).toHaveLength(2);
    for (const date of dates) expect(date.className).toContain("text-danger");

    const todayRow = within(group("Today")).getByRole("listitem");
    for (const date of within(todayRow).getAllByText("Today")) expect(date.className).not.toContain("text-danger");
    // No form view and no display name: the record id, as plain text.
    expect(within(todayRow).getByText("n1").closest("a")).toBeNull();
  });

  it("marks an activity done with feedback and removes its row", async () => {
    const { completions } = seed([
      { dueDate: today, summary: "Confirm delivery" },
      { dueDate: addDays(today, 1), summary: "Other" },
    ]);
    await renderPage();

    fireEvent.click(await screen.findByRole("button", { name: 'Mark "Confirm delivery" done' }));
    fireEvent.change(screen.getByRole("textbox", { name: 'Feedback for "Confirm delivery"' }), {
      target: { value: "  Customer confirmed Friday.  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Done" }));

    await waitFor(() => expect(screen.queryByText("Confirm delivery")).toBeNull());
    expect(completions).toEqual([{ id: "s0001", body: { feedback: "Customer confirmed Friday." } }]);
    expect(screen.queryByRole("region", { name: "Today" })).toBeNull();
    expect(screen.getByText("Other")).toBeTruthy();
  });

  it("marks done with no feedback from the keyboard, and Escape closes the box", async () => {
    const { completions } = seed([{ dueDate: today, summary: "Confirm delivery" }]);
    await renderPage();

    fireEvent.click(await screen.findByRole("button", { name: 'Mark "Confirm delivery" done' }));
    const box = screen.getByRole("textbox", { name: 'Feedback for "Confirm delivery"' });
    fireEvent.keyDown(box, { key: "Escape" });
    expect(screen.queryByRole("textbox")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: 'Mark "Confirm delivery" done' }));
    fireEvent.keyDown(screen.getByRole("textbox", { name: 'Feedback for "Confirm delivery"' }), { key: "Enter" });

    await waitFor(() => expect(completions).toEqual([{ id: "s0001", body: {} }]));
    await waitFor(() => expect(screen.getByText("Nothing planned")).toBeTruthy());
  });

  it("ignores Enter and Escape while a completion is in flight", async () => {
    seed([{ dueDate: today, summary: "Confirm delivery" }]);
    const held: (() => void)[] = [];
    const post = apiClient.post;
    apiClient.post = (async (path: string, body?: unknown) => {
      await new Promise<void>((resolve) => held.push(resolve));
      return post(path, body);
    }) as typeof apiClient.post;
    await renderPage();

    fireEvent.click(await screen.findByRole("button", { name: 'Mark "Confirm delivery" done' }));
    const box = screen.getByRole("textbox", { name: 'Feedback for "Confirm delivery"' });
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(screen.getByRole("button", { name: "Done" }).hasAttribute("disabled")).toBe(true));
    fireEvent.keyDown(box, { key: "Enter" });
    fireEvent.keyDown(box, { key: "Escape" });
    expect(screen.getByRole("textbox", { name: 'Feedback for "Confirm delivery"' })).toBeTruthy();
    expect(held).toHaveLength(1);

    for (const release of held) release();
    await waitFor(() => expect(screen.queryByText("Confirm delivery")).toBeNull());
  });

  it("keeps the row and explains a failed completion", async () => {
    vi.spyOn(toast, "error");
    const { failNextDone } = seed([{ dueDate: today, summary: "Confirm delivery" }]);
    failNextDone(new AppError({ code: "not_participant", message: "no", httpStatus: 403 }));
    await renderPage();

    fireEvent.click(await screen.findByRole("button", { name: 'Mark "Confirm delivery" done' }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("You can no longer change this activity."));
    expect(screen.getByText("Confirm delivery")).toBeTruthy();
    expect(screen.getByRole("textbox", { name: 'Feedback for "Confirm delivery"' })).toBeTruthy();
  });

  it("loads more pages on request", async () => {
    seed(
      Array.from({ length: 55 }, (_, i) => ({ dueDate: addDays(today, 1 + Math.floor(i / 10)), summary: `Task ${i}` })),
    );
    await renderPage();

    await waitFor(() => expect(within(group("Upcoming")).getAllByRole("listitem")).toHaveLength(50));
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(within(group("Upcoming")).getAllByRole("listitem")).toHaveLength(55));
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("shows the empty state when nothing is assigned", async () => {
    seed([]);
    await renderPage();

    expect(await screen.findByText("Nothing planned")).toBeTruthy();
    expect(screen.getByText("Activities assigned to you on any record will show up here.")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
