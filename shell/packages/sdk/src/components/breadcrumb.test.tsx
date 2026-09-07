import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Breadcrumb } from "./breadcrumb.js";

afterEach(cleanup);

describe("Breadcrumb", () => {
  it("renders every item's label", () => {
    const items = [
      { label: "Documents", onClick: () => {} },
      { label: "2026", onClick: () => {} },
      { label: "Invoices" },
    ];
    render(<Breadcrumb items={items} />);
    expect(screen.getByText("Documents")).toBeTruthy();
    expect(screen.getByText("2026")).toBeTruthy();
    expect(screen.getByText("Invoices")).toBeTruthy();
  });

  it("renders items with an onClick as clickable buttons", () => {
    const onClick = vi.fn();
    render(<Breadcrumb items={[{ label: "Documents", onClick }, { label: "Invoices" }]} />);
    fireEvent.click(screen.getByRole("button", { name: "Documents" }));
    expect(onClick).toHaveBeenCalled();
  });

  it("renders an item without onClick as plain text, not a link or button", () => {
    render(<Breadcrumb items={[{ label: "Documents", onClick: () => {} }, { label: "Invoices" }]} />);
    expect(screen.queryByRole("button", { name: "Invoices" })).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("marks the last item as the current page", () => {
    render(<Breadcrumb items={[{ label: "Documents", onClick: () => {} }, { label: "Invoices" }]} />);
    expect(screen.getByText("Invoices").getAttribute("aria-current")).toBe("page");
    expect(screen.getByText("Documents").getAttribute("aria-current")).toBeNull();
  });

  it("never renders the last item as interactive, even when it has an onClick", () => {
    render(
      <Breadcrumb
        items={[
          { label: "Documents", onClick: () => {} },
          { label: "Invoices", onClick: () => {} },
        ]}
      />,
    );
    expect(screen.queryByRole("button", { name: "Invoices" })).toBeNull();
    expect(screen.getByText("Invoices").getAttribute("aria-current")).toBe("page");
  });
});
