import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TimelineChart } from "./timeline-chart.js";

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
    rows: [],
    range: { start: new Date(2026, 4, 1), end: new Date(2026, 4, 31) },
    rangeMode: "month" as const,
    onRangeModeChange: vi.fn(),
    onNavigate: vi.fn(),
    onBarChange: vi.fn(async () => {}),
    label: "Project Timeline",
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
});
