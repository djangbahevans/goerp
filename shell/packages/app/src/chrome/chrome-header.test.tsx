import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChromeHeader } from "./chrome-header.js";

vi.mock("./route-breadcrumb.js", () => ({ RouteBreadcrumb: () => <nav aria-label="Breadcrumb" /> }));
vi.mock("./search-trigger.js", () => ({ SearchTrigger: () => <button type="button">search</button> }));
vi.mock("./notification-bell.js", () => ({ NotificationBell: () => <button type="button">notifications</button> }));
vi.mock("./user-menu.js", () => ({ UserMenu: () => <button type="button">account</button> }));

afterEach(cleanup);

describe("ChromeHeader", () => {
  it("renders a banner landmark composing the breadcrumb and the end-edge action cluster", () => {
    render(<ChromeHeader />);

    const header = screen.getByRole("banner");
    expect(header).toBeTruthy();
    expect(screen.getByRole("navigation", { name: "Breadcrumb" })).toBeTruthy();
    expect(screen.getByText("search")).toBeTruthy();
    expect(screen.getByText("notifications")).toBeTruthy();
    expect(screen.getByText("account")).toBeTruthy();
  });
});
