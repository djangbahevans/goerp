import { useUnreadCount } from "@goerp/sdk/notifications";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NotificationBell } from "./notification-bell.js";

vi.mock("@goerp/sdk/notifications", () => ({
  useUnreadCount: vi.fn(),
}));

vi.mock("./notification-sheet.js", () => ({
  NotificationSheet: ({ open, onClose }: { open: boolean; onClose: () => void }) => (
    <div data-testid="sheet" data-open={open}>
      <button type="button" onClick={onClose}>
        close from sheet
      </button>
    </div>
  ),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("NotificationBell", () => {
  it("shows no badge when there are no unread notifications", () => {
    vi.mocked(useUnreadCount).mockReturnValue({ count: 0 });
    render(<NotificationBell />);

    expect(screen.getByRole("button", { name: "Notifications" })).toBeTruthy();
  });

  it("includes the unread count in the accessible name, not just the badge", () => {
    vi.mocked(useUnreadCount).mockReturnValue({ count: 3 });
    render(<NotificationBell />);

    expect(screen.getByRole("button", { name: "Notifications (3 unread)" })).toBeTruthy();
    expect(screen.getByText("3")).toBeTruthy();
  });

  it("caps the visible badge count at 9+", () => {
    vi.mocked(useUnreadCount).mockReturnValue({ count: 42 });
    render(<NotificationBell />);

    expect(screen.getByText("9+")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Notifications (42 unread)" })).toBeTruthy();
  });

  it("toggles the sheet open on click", () => {
    vi.mocked(useUnreadCount).mockReturnValue({ count: 0 });
    render(<NotificationBell />);

    expect(screen.getByTestId("sheet").dataset.open).toBe("false");
    fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
    expect(screen.getByTestId("sheet").dataset.open).toBe("true");
  });

  it("closes when the sheet calls onClose", () => {
    vi.mocked(useUnreadCount).mockReturnValue({ count: 0 });
    render(<NotificationBell />);

    fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
    fireEvent.click(screen.getByText("close from sheet"));
    expect(screen.getByTestId("sheet").dataset.open).toBe("false");
  });
});
