import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { StatCard } from "./stat-card.js";

afterEach(cleanup);

describe("StatCard", () => {
  it("renders the label and value", () => {
    render(<StatCard label="Total Contacts" value={4823} />);
    expect(screen.getByText("Total Contacts")).toBeTruthy();
    expect(screen.getByText("4823")).toBeTruthy();
  });

  it("renders the change delta with its period", () => {
    render(
      <StatCard label="Total Contacts" value={4823} change={{ value: 12, direction: "up", period: "this month" }} />,
    );
    expect(screen.getByText(/12 this month/)).toBeTruthy();
  });

  it("renders no change delta when omitted", () => {
    render(<StatCard label="Total Contacts" value={4823} />);
    expect(screen.queryByText(/this month/)).toBeNull();
  });

  it("wraps the card in a link when href is provided", () => {
    render(<StatCard label="Total Contacts" value={4823} href="/contacts" />);
    const link = screen.getByRole("link");
    expect(link.getAttribute("href")).toBe("/contacts");
    expect(link.textContent).toContain("Total Contacts");
  });

  it("keys the visible focus ring off the actual focusable link, not an inert inner div", () => {
    render(<StatCard label="Total Contacts" value={4823} href="/contacts" />);
    const link = screen.getByRole("link");
    expect(link.className).toContain("group");
    expect(link.querySelector("div")?.className).toContain("group-focus-visible:shadow-focus");
  });

  it("does not render a link when href is omitted", () => {
    render(<StatCard label="Total Contacts" value={4823} />);
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("renders a skeleton in place of the value when value is undefined", () => {
    const { container } = render(<StatCard label="Total Contacts" value={undefined} />);
    expect(container.querySelector('[data-skeleton="lines"]')).toBeTruthy();
    expect(screen.queryByText("4823")).toBeNull();
  });

  it("formats the value as currency from integer minor units when format is currency", () => {
    render(<StatCard label="Revenue" value={10000} format="currency" currency="GHS" />);
    const expected = new Intl.NumberFormat(undefined, { style: "currency", currency: "GHS" }).format(100);
    expect(screen.getByText("Revenue").nextElementSibling?.textContent).toBe(expected);
  });

  it("tints the value for an at-risk color", () => {
    render(<StatCard label="Out of Stock" value={3} color="red" />);
    expect(screen.getByText("3").className).toContain("text-danger");
  });
});
