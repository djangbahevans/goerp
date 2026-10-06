import type { Notification, UseNotificationsResult } from "@goerp/sdk/notifications";
import { useMarkAllRead, useMarkRead, useNotifications, useUnreadCount } from "@goerp/sdk/notifications";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationItem, NotificationSheet } from "./notification-sheet.js";

vi.mock("@goerp/sdk/notifications", () => ({
  useNotifications: vi.fn(),
  useMarkRead: vi.fn(),
  useMarkAllRead: vi.fn(),
  useUnreadCount: vi.fn(),
}));

function fakeNotification(overrides: Partial<Notification> = {}): Notification {
  return {
    id: "n1",
    type: "sales.order_confirmed",
    module: "sales",
    title: "Order confirmed",
    body: null,
    actionUrl: null,
    icon: null,
    readAt: null,
    createdAt: new Date().toISOString(),
    ...overrides,
  };
}

function feedResult(overrides: Partial<UseNotificationsResult> = {}): UseNotificationsResult {
  return {
    notifications: [],
    isLoading: false,
    hasMore: false,
    isFetchingNextPage: false,
    fetchMore: vi.fn(),
    ...overrides,
  };
}

async function renderSheet(open = true, onClose = vi.fn()) {
  const rootRoute = createRootRoute({ component: () => <NotificationSheet open={open} onClose={onClose} /> });
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => null });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return { onClose };
}

const markRead = vi.fn();
const markAllReadMutate = vi.fn();

function markAllReadState(overrides: Record<string, unknown> = {}) {
  vi.mocked(useMarkAllRead).mockReturnValue({
    mutate: markAllReadMutate,
    reset: vi.fn(),
    isPending: false,
    isError: false,
    isSuccess: false,
    ...overrides,
  } as unknown as ReturnType<typeof useMarkAllRead>);
}

beforeEach(() => {
  vi.mocked(useMarkRead).mockReturnValue({ mutate: markRead } as unknown as ReturnType<typeof useMarkRead>);
  vi.mocked(useUnreadCount).mockReturnValue({ count: 2 });
  markAllReadState();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("NotificationSheet", () => {
  it("shows a loading skeleton on first open", async () => {
    vi.mocked(useNotifications).mockReturnValue(feedResult({ isLoading: true }));
    await renderSheet();

    expect(screen.getByRole("dialog", { name: "Notifications" })).toBeTruthy();
    expect(document.querySelector('[data-skeleton], [aria-busy="true"]')).toBeTruthy();
  });

  it("shows EmptyState when loaded with no notifications", async () => {
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    await renderSheet();

    expect(screen.getByText("No notifications yet")).toBeTruthy();
  });

  it("moves focus to the panel's own heading on open, not the trigger or first row", async () => {
    vi.mocked(useNotifications).mockReturnValue(feedResult({ notifications: [fakeNotification()] }));
    await renderSheet();

    await waitFor(() => expect(screen.getByText("Notifications")).toBe(document.activeElement));
  });

  it("gives an unread notification a filled dot and the read one none", async () => {
    vi.mocked(useNotifications).mockReturnValue(
      feedResult({
        notifications: [
          fakeNotification({ id: "unread", title: "Unread item", readAt: null }),
          fakeNotification({ id: "read", title: "Read item", readAt: new Date().toISOString() }),
        ],
      }),
    );
    await renderSheet();

    const unreadRow = screen.getByText("Unread item").closest("[data-notification-id]");
    const readRow = screen.getByText("Read item").closest("[data-notification-id]");
    expect(unreadRow?.querySelector('[aria-hidden="true"]')).toBeTruthy();
    expect(readRow?.querySelector('[aria-hidden="true"]')).toBeNull();
  });

  it("renders a notification without an action_url as non-interactive", async () => {
    vi.mocked(useNotifications).mockReturnValue(
      feedResult({ notifications: [fakeNotification({ title: "Informational", actionUrl: null })] }),
    );
    await renderSheet();

    const row = screen.getByText("Informational").closest("[data-notification-id]");
    expect(row?.tagName).toBe("DIV");
  });

  it("clicking a notification with an action_url marks it read, navigates, and closes the sheet", async () => {
    vi.mocked(useNotifications).mockReturnValue(
      feedResult({ notifications: [fakeNotification({ id: "n2", title: "Go here", actionUrl: "/inbox" })] }),
    );
    const { onClose } = await renderSheet();

    fireEvent.click(screen.getByText("Go here"));

    expect(markRead).toHaveBeenCalledWith("n2");
    expect(onClose).toHaveBeenCalled();
  });

  it("fetches the next page when scrolled near the bottom", async () => {
    const fetchMore = vi.fn();
    vi.mocked(useNotifications).mockReturnValue(
      feedResult({ notifications: [fakeNotification()], hasMore: true, fetchMore }),
    );
    await renderSheet();

    const list = document.querySelector(".overflow-y-auto") as HTMLDivElement;
    Object.defineProperty(list, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(list, "clientHeight", { value: 400, configurable: true });
    Object.defineProperty(list, "scrollTop", { value: 550, configurable: true });
    fireEvent.scroll(list);

    expect(fetchMore).toHaveBeenCalled();
  });

  it("shows a trailing spinner row while fetching the next page", async () => {
    vi.mocked(useNotifications).mockReturnValue(
      feedResult({ notifications: [fakeNotification()], isFetchingNextPage: true, hasMore: true }),
    );
    await renderSheet();

    expect(screen.getByText("Loading more…")).toBeTruthy();
  });

  it("calls onClose on Escape", async () => {
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    const { onClose } = await renderSheet();

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("stays rendered through the close animation, then unmounts", async () => {
    vi.useFakeTimers();
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    let setOpen: (open: boolean) => void = () => {};
    function Wrapper() {
      const [open, setOpenState] = useState(true);
      setOpen = setOpenState;
      return <NotificationSheet open={open} onClose={vi.fn()} />;
    }
    const rootRoute = createRootRoute({ component: Wrapper });
    const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => null });
    const router = createRouter({
      routeTree: rootRoute.addChildren([indexRoute]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    await router.load();
    render(<RouterProvider router={router} />);

    expect(screen.getByRole("dialog")).toBeTruthy();

    act(() => setOpen(false));
    expect(screen.getByRole("dialog")).toBeTruthy();

    act(() => vi.advanceTimersByTime(150));
    expect(screen.queryByRole("dialog")).toBeNull();

    vi.useRealTimers();
  });
});

describe("NotificationSheet header row", () => {
  it("opens on All and requests the unread feed once Unread is chosen", async () => {
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    await renderSheet();

    expect((screen.getByRole("radio", { name: "All" }) as HTMLInputElement).checked).toBe(true);
    expect(useNotifications).toHaveBeenLastCalledWith({ limit: 20, unread: false });

    fireEvent.click(screen.getByRole("radio", { name: "Unread" }));

    expect(useNotifications).toHaveBeenLastCalledWith({ limit: 20, unread: true });
  });

  it("shows the caught-up empty state in the Unread view only", async () => {
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    await renderSheet();
    expect(screen.getByText("No notifications yet")).toBeTruthy();

    fireEvent.click(screen.getByRole("radio", { name: "Unread" }));

    expect(screen.getByText("You're all caught up.")).toBeTruthy();
  });

  it("goes back to All each time the sheet reopens", async () => {
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    let setOpen: (open: boolean) => void = () => {};
    function Wrapper() {
      const [open, setOpenState] = useState(true);
      setOpen = setOpenState;
      return <NotificationSheet open={open} onClose={vi.fn()} />;
    }
    const rootRoute = createRootRoute({ component: Wrapper });
    const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => null });
    const router = createRouter({
      routeTree: rootRoute.addChildren([indexRoute]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    await router.load();
    render(<RouterProvider router={router} />);

    fireEvent.click(screen.getByRole("radio", { name: "Unread" }));
    act(() => setOpen(false));
    act(() => setOpen(true));

    expect((screen.getByRole("radio", { name: "All" }) as HTMLInputElement).checked).toBe(true);
  });

  it("marks everything read from the button", async () => {
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    await renderSheet();

    fireEvent.click(screen.getByRole("button", { name: "Mark all as read" }));

    expect(markAllReadMutate).toHaveBeenCalledOnce();
  });

  it("disables the button when nothing is unread", async () => {
    vi.mocked(useUnreadCount).mockReturnValue({ count: 0 });
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    await renderSheet();

    expect((screen.getByRole("button", { name: "Mark all as read" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows the button as busy while marking", async () => {
    markAllReadState({ isPending: true });
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    await renderSheet();

    expect(screen.getByRole("button", { name: /Mark all as read/ }).getAttribute("aria-busy")).toBe("true");
  });

  it("announces success in a status region", async () => {
    markAllReadState({ isSuccess: true });
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    await renderSheet();

    expect(screen.getByRole("status").textContent).toBe("All notifications marked as read.");
  });

  it("shows an alert line when marking fails", async () => {
    markAllReadState({ isError: true });
    vi.mocked(useNotifications).mockReturnValue(feedResult());
    await renderSheet();

    expect(screen.getByRole("alert").textContent).toBe("Couldn't mark notifications as read.");
  });
});

describe("NotificationItem", () => {
  it("renders a known icon in a slot reserved before its glyph loads", async () => {
    const { container } = render(<NotificationItem notification={fakeNotification({ icon: "shopping-cart" })} />);
    const slot = container.querySelector("span.size-4");
    expect(slot).not.toBeNull();
    await waitFor(() => expect(slot?.querySelector("svg")).not.toBeNull());
  });

  it("reserves no slot for an unknown icon name", () => {
    const { container } = render(<NotificationItem notification={fakeNotification({ icon: "not-an-icon" })} />);
    expect(container.querySelector("span.size-4")).toBeNull();
    expect(screen.getByText("Order confirmed")).toBeTruthy();
  });

  it("renders as plain content with no action URL", () => {
    render(<NotificationItem notification={fakeNotification()} />);
    expect(screen.queryByRole("button")).toBeNull();
  });
});
