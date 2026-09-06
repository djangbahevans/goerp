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

  it("does not render a link when href is omitted", () => {
    render(<StatCard label="Total Contacts" value={4823} />);
    expect(screen.queryByRole("link")).toBeNull();
  });
});
