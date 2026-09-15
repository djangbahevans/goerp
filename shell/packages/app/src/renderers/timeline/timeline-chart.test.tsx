import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TimelineChart } from "./timeline-chart.js";
import type { TimelineRowData } from "./timeline-view-types.js";

afterEach(cleanup);

const permissionValue = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
});

function renderWithPermissions(element: ReactElement) {
  return render(<PermissionContext.Provider value={permissionValue}>{element}</PermissionContext.Provider>);
}

function baseProps() {
  return {
    rows: [] as TimelineRowData[],
    range: { start: new Date(2026, 4, 1), end: new Date(2026, 4, 31) },
    rangeMode: "month" as const,
    onRangeModeChange: vi.fn(),
    onNavigate: vi.fn(),
    onBarChange: vi.fn(async () => {}),
    label: "Project Timeline",
  };
}

function oneBarRow(): TimelineRowData {
  return {
    id: "",
    label: "",
    laneCount: 1,
    bars: [
      {
        id: "t1",
        label: "Design review",
        start: new Date(2026, 4, 10),
        end: new Date(2026, 4, 14),
        clamped: false,
        lane: 0,
      },
    ],
  };
}

describe("TimelineChart", () => {
  it("switching the range-mode tab calls onRangeModeChange", () => {
    const props = baseProps();
    renderWithPermissions(<TimelineChart {...props} />);
    fireEvent.click(screen.getByRole("tab", { name: "Week" }));
    expect(props.onRangeModeChange).toHaveBeenCalledWith("week");
  });

  it("previous/today/next call onNavigate with -1/0/1", () => {
    const props = baseProps();
    renderWithPermissions(<TimelineChart {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Previous" }));
    fireEvent.click(screen.getByRole("button", { name: "Today" }));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(props.onNavigate).toHaveBeenNthCalledWith(1, -1);
    expect(props.onNavigate).toHaveBeenNthCalledWith(2, 0);
    expect(props.onNavigate).toHaveBeenNthCalledWith(3, 1);
  });

  it("carries the view label into the grid's accessible name", () => {
    renderWithPermissions(<TimelineChart {...baseProps()} />);
    expect(screen.getByRole("group", { name: "Timeline: Project Timeline" })).toBeTruthy();
  });

  it("keyboard: a nudge followed by Enter commits via onBarChange with the nudged dates", async () => {
    const props = { ...baseProps(), rows: [oneBarRow()] };
    renderWithPermissions(<TimelineChart {...props} />);
    const bar = screen.getByRole("group", { name: /Design review/ });
    fireEvent.keyDown(bar, { key: "ArrowRight" });
    fireEvent.keyDown(bar, { key: "Enter" });
    await waitFor(() =>
      expect(props.onBarChange).toHaveBeenCalledWith({
        id: "t1",
        start: new Date(2026, 4, 11),
        end: new Date(2026, 4, 15),
      }),
    );
  });

  it("keyboard: a nudge followed by Escape never calls onBarChange", () => {
    const props = { ...baseProps(), rows: [oneBarRow()] };
    renderWithPermissions(<TimelineChart {...props} />);
    const bar = screen.getByRole("group", { name: /Design review/ });
    fireEvent.keyDown(bar, { key: "ArrowRight" });
    fireEvent.keyDown(bar, { key: "Escape" });
    expect(props.onBarChange).not.toHaveBeenCalled();
  });

  it("a failed commit reverts the bar's displayed position and shows a toast", async () => {
    const { toast } = await import("@goerp/sdk/notifications");
    const toastSpy = vi.spyOn(toast, "error").mockImplementation(() => {});
    const props = {
      ...baseProps(),
      rows: [oneBarRow()],
      onBarChange: vi.fn(async () => Promise.reject(new Error("boom"))),
    };
    renderWithPermissions(<TimelineChart {...props} />);
    const bar = screen.getByRole("group", { name: /Design review/ });
    fireEvent.keyDown(bar, { key: "ArrowRight" });
    fireEvent.keyDown(bar, { key: "Enter" });

    await waitFor(() => expect(toastSpy).toHaveBeenCalled());
    expect(screen.getByRole("group", { name: /May 10.*May 14/ })).toBeTruthy();
  });

  it("pointer: a drag-and-drop (even in place) commits via onBarChange", async () => {
    const props = { ...baseProps(), rows: [oneBarRow()] };
    renderWithPermissions(<TimelineChart {...props} />);
    const bar = screen.getByRole("group", { name: /Design review/ }) as HTMLElement & {
      setPointerCapture: (id: number) => void;
    };
    bar.setPointerCapture = vi.fn();
    fireEvent.pointerDown(bar, { clientX: 100, pointerId: 1 });
    fireEvent.pointerUp(bar);
    await waitFor(() =>
      expect(props.onBarChange).toHaveBeenCalledWith({
        id: "t1",
        start: new Date(2026, 4, 10),
        end: new Date(2026, 4, 14),
      }),
    );
  });
});
