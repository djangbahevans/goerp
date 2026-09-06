import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Breadcrumb } from "./breadcrumb.js";

afterEach(cleanup);

const items = [
  { label: "Contacts", href: "/contacts" },
  { label: "Companies", href: "/contacts/companies" },
  { label: "Acme Inc" },
];

describe("Breadcrumb", () => {
  it("renders every item's label", () => {
    render(<Breadcrumb items={items} />);
    expect(screen.getByText("Contacts")).toBeTruthy();
    expect(screen.getByText("Companies")).toBeTruthy();
    expect(screen.getByText("Acme Inc")).toBeTruthy();
  });

  it("renders non-final items with an href as links", () => {
    render(<Breadcrumb items={items} />);
    expect(screen.getByRole("link", { name: "Contacts" }).getAttribute("href")).toBe("/contacts");
    expect(screen.getByRole("link", { name: "Companies" }).getAttribute("href")).toBe("/contacts/companies");
  });

  it("renders the last item as plain text marked as the current page, even with an href", () => {
    const withFinalHref = [...items.slice(0, -1), { label: "Acme Inc", href: "/contacts/acme" }];
    render(<Breadcrumb items={withFinalHref} />);
    expect(screen.queryByRole("link", { name: "Acme Inc" })).toBeNull();
    expect(screen.getByText("Acme Inc").getAttribute("aria-current")).toBe("page");
  });
});
